// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	agentlib "github.com/bborbe/agent"
	task "github.com/bborbe/agent/command/task"
	"github.com/bborbe/errors"
	"github.com/bborbe/github-build-watcher/pkg/filter"
	"github.com/bborbe/github-build-watcher/pkg/maintenance"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
	"github.com/google/uuid"
)

//counterfeiter:generate -o ../mocks/watcher.go --fake-name Watcher . Watcher

// DependabotGraphUpdatePrefixes are workflow-name prefixes used by Dependabot for
// internal graph-maintenance jobs. These are NOT real CI failures — their HTTP 503s
// are Dependabot's own service being temporarily flaky. The real CI workflows on
// the same commits succeed. These runs must not trigger OpenClaw build-failure tasks.
var DependabotGraphUpdatePrefixes = []string{
	"Graph Update:",
	"Dependabot Updates",
}

// isDependabotGraphUpdateWorkflow returns true when run.Name starts with any
// prefix in DependabotGraphUpdatePrefixes. Comparison is case-sensitive.
// An empty or zero Name is NOT considered a Dependabot workflow — returns false.
func isDependabotGraphUpdateWorkflow(run WorkflowRun) bool {
	if run.Name == "" {
		return false
	}
	for _, prefix := range DependabotGraphUpdatePrefixes {
		if strings.HasPrefix(run.Name, prefix) {
			return true
		}
	}
	return false
}

// Watcher polls GitHub Actions for build status changes.
//
// When force is true, the red×red episode-lock arm of the state machine
// publishes a salted CreateTaskCommand instead of skipping (spec 069), so
// operators can force a re-publish for a still-red build via the /trigger
// HTTP path. The poll-interval loop always passes false.
type Watcher interface {
	Poll(ctx context.Context, force bool) error
}

// AllowlistSnapshot returns the current set of concrete "host/owner/repo"
// entries the poll loop should iterate. Implementations MUST be safe to
// call from a goroutine concurrent with a refresh writer.
type AllowlistSnapshot interface {
	Snapshot() []string
}

// StaticSnapshot is an AllowlistSnapshot backed by an immutable slice.
// Used by the pure-literal binary path so no wildcard machinery runs.
type StaticSnapshot struct {
	entries []string
}

// NewStaticSnapshot returns a snapshot holding a defensive copy of entries.
func NewStaticSnapshot(entries []string) *StaticSnapshot {
	return &StaticSnapshot{entries: append([]string(nil), entries...)}
}

// Snapshot returns the held entry slice. Callers MUST NOT mutate it.
func (s *StaticSnapshot) Snapshot() []string { return s.entries }

// NewWatcher returns a Watcher that polls GitHub Actions and publishes commands.
func NewWatcher(
	githubClient GitHubClient,
	createSender task.CreateCommandSender,
	metrics Metrics,
	repoFilter filter.RepoFilter,
	allowlist AllowlistSnapshot,
	cursorPath string,
	assignee string,
	taskStatus string,
	taskPhase string,
	maintenanceLoader maintenance.Loader,
	maxTitleLen int,
	taskSuffix string,
	currentDateTime libtime.CurrentDateTimeGetter,
) Watcher {
	return &buildWatcher{
		githubClient:      githubClient,
		createSender:      createSender,
		metrics:           metrics,
		repoFilter:        repoFilter,
		allowlist:         allowlist,
		cursorPath:        cursorPath,
		assignee:          assignee,
		taskStatus:        taskStatus,
		taskPhase:         taskPhase,
		maintenanceLoader: maintenanceLoader,
		maxTitleLen:       maxTitleLen,
		taskSuffix:        taskSuffix,
		currentDateTime:   currentDateTime,
	}
}

type buildWatcher struct {
	githubClient      GitHubClient
	createSender      task.CreateCommandSender
	metrics           Metrics
	repoFilter        filter.RepoFilter
	allowlist         AllowlistSnapshot
	cursorPath        string
	assignee          string
	taskStatus        string
	taskPhase         string
	maintenanceLoader maintenance.Loader
	maxTitleLen       int
	taskSuffix        string
	currentDateTime   libtime.CurrentDateTimeGetter
}

func (w *buildWatcher) Poll(ctx context.Context, force bool) error {
	cursor, err := LoadCursor(ctx, w.cursorPath)
	if err != nil {
		return errors.Wrapf(ctx, err, "load cursor")
	}

	snapshot := w.allowlist.Snapshot()
	for _, repoKey := range snapshot {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if w.repoFilter.Skip(repoKey) {
			glog.V(3).Infof("skipping repo=%s reason=filtered", repoKey)
			continue
		}

		if rateLimited := w.pollRepo(ctx, cursor, repoKey, force); rateLimited {
			break
		}
	}

	var redCount float64
	for _, state := range cursor.Repos {
		if state.LastKnownState == "red" {
			redCount++
		}
	}
	w.metrics.SetCurrentRedRepos(redCount)

	if err := SaveCursor(ctx, w.cursorPath, cursor); err != nil {
		glog.Warningf("cursor save failed: %v", err)
	}

	w.metrics.IncPollCycle("success")
	return nil
}

// pollRepo processes one repo. Returns true when the outer loop should break (rate-limited).
func (w *buildWatcher) pollRepo(
	ctx context.Context,
	cursor *Cursor,
	repoKey string,
	force bool,
) bool {
	owner, repo := splitRepoKey(repoKey)
	repoState := GetOrCreateRepoState(cursor, repoKey)

	if repoState.DefaultBranch == "" {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		branch, err := w.githubClient.GetDefaultBranch(ctx, owner, repo)
		if err != nil {
			glog.Warningf("get default branch failed repo=%s err=%v", repoKey, err)
			w.metrics.IncPollError("github_error")
			return false
		}
		repoState.DefaultBranch = branch
	}

	select {
	case <-ctx.Done():
		return false
	default:
	}
	runs, err := w.githubClient.GetWorkflowRuns(ctx, owner, repo, repoState.DefaultBranch)
	if err != nil {
		if errors.Is(err, ErrRateLimited) {
			w.metrics.IncPollError("rate_limited")
			return true
		}
		glog.Warningf("get workflow runs failed repo=%s err=%v", repoKey, err)
		w.metrics.IncPollError("github_error")
		return false
	}
	w.metrics.IncReposChecked()

	currState, episodeSHA, failingRuns := deriveState(runs)
	if currState == "undefined" {
		return false
	}

	w.applyStateMachine(
		ctx,
		repoKey,
		repoState,
		currState,
		episodeSHA,
		failingRuns,
		owner,
		repo,
		force,
	)
	return false
}

// applyStateMachine applies the green/red state machine for a single repo.
//
// When force is true and prevState==currState=="red", the episode-lock skip is
// bypassed and a salted CreateTaskCommand is published (spec 069). All other
// arms ignore force.
func (w *buildWatcher) applyStateMachine(
	ctx context.Context,
	repoKey string,
	repoState *RepoState,
	currState, episodeSHA string,
	failingRuns []WorkflowRun,
	owner, repo string,
	force bool,
) {
	prevState := repoState.LastKnownState

	switch {
	case (prevState == "" || prevState == "green") && currState == "red":
		overrides := w.maintenanceLoader.LoadOverrides(ctx, owner, repo, repoState.DefaultBranch)
		effectiveAssignee := coalesceString(overrides.Assignee, w.assignee)
		effectiveStatus := coalesceString(overrides.Status, w.taskStatus)
		effectivePhase := coalesceString(overrides.Phase, w.taskPhase)
		taskID := DeriveTaskID(owner, repo, episodeSHA)
		cmd := w.buildCreateTaskCommand(
			ctx,
			taskID,
			owner,
			repo,
			episodeSHA,
			failingRuns,
			effectiveAssignee,
			effectiveStatus,
			effectivePhase,
			overrides.IncludeLogs,
		)
		if err := w.createSender.SendCommand(ctx, cmd); err != nil {
			glog.Errorf("publish create-task failed repo=%s err=%v", repoKey, err)
			w.metrics.IncPollError("kafka_error")
			return // do NOT update cursor — next poll retries
		}
		w.metrics.IncTaskPublished()
		w.metrics.IncStateTransition("green_to_red")
		repoState.LastKnownState = "red"
		repoState.CurrentEpisodeSHA = episodeSHA

	case prevState == "red" && currState == "red" && force:
		// Spec 069: force=true on a still-red repo re-publishes with a salted
		// TaskIdentifier so the agent controller's file-exists skip does NOT
		// fire and a fresh vault task is created. Cursor state stays "red";
		// CurrentEpisodeSHA is unchanged (the episode is the same; only the
		// task identifier is salted to evade dedup).
		overrides := w.maintenanceLoader.LoadOverrides(ctx, owner, repo, repoState.DefaultBranch)
		effectiveAssignee := coalesceString(overrides.Assignee, w.assignee)
		effectiveStatus := coalesceString(overrides.Status, w.taskStatus)
		effectivePhase := coalesceString(overrides.Phase, w.taskPhase)
		nonce := strconv.FormatInt(w.currentDateTime.Now().UnixMicro(), 10)
		taskID := DeriveTaskIDForce(owner, repo, episodeSHA, nonce)
		cmd := w.buildCreateTaskCommand(
			ctx,
			taskID,
			owner,
			repo,
			episodeSHA,
			failingRuns,
			effectiveAssignee,
			effectiveStatus,
			effectivePhase,
			overrides.IncludeLogs,
		)
		if err := w.createSender.SendCommand(ctx, cmd); err != nil {
			glog.Errorf("publish create-task (force) failed repo=%s err=%v", repoKey, err)
			w.metrics.IncPollError("kafka_error")
			return
		}
		w.metrics.IncTaskPublished()
		// NOTE: IncStateTransition("green_to_red") is NOT incremented on the force
		// path — the state didn't actually transition (it was already red), so
		// labeling it as green_to_red would be misleading. Force publishes count
		// only toward IncTaskPublished (spec 069).

	case prevState == "red" && currState == "red":
		// Episode locked on first red; skip regardless of SHA change (force=false)

	case prevState == "red" && currState == "green":
		w.metrics.IncStateTransition("red_to_green")
		repoState.LastKnownState = "green"
		repoState.CurrentEpisodeSHA = ""

	default:
		// (prevState == "" || prevState == "green") && currState == "green": no transition
	}
}

// deriveState computes the current build state for a repo from its workflow runs.
// Returns state ("green"|"red"|"undefined"), episodeSHA (only when red), and the failing runs.
func deriveState(runs []WorkflowRun) (state string, episodeSHA string, failingRuns []WorkflowRun) {
	// Group by WorkflowID, keep only the latest run per workflow (by CreatedAt desc)
	latestByWorkflow := make(map[int64]WorkflowRun)
	for _, run := range runs {
		existing, ok := latestByWorkflow[run.WorkflowID]
		if !ok || run.CreatedAt.After(existing.CreatedAt) {
			latestByWorkflow[run.WorkflowID] = run
		}
	}

	// Filter: only "failure" or "success" conclusions
	var considered []WorkflowRun
	for _, run := range latestByWorkflow {
		if run.Conclusion == "failure" || run.Conclusion == "success" {
			// Skip Dependabot internal graph-maintenance workflows.
			// They are not real CI and must not affect the red/green state machine.
			if isDependabotGraphUpdateWorkflow(run) {
				glog.V(4).
					Infof("skipping workflow run id=%d name=%q (Dependabot graph-update)", run.RunID, run.Name)
				continue
			}
			considered = append(considered, run)
		}
	}

	if len(considered) == 0 {
		return "undefined", "", nil
	}

	for _, run := range considered {
		if run.Conclusion == "failure" {
			failingRuns = append(failingRuns, run)
		}
	}

	if len(failingRuns) == 0 {
		return "green", "", nil
	}

	// Episode SHA = HeadSHA of the earliest (smallest CreatedAt) failing run
	sort.Slice(failingRuns, func(i, j int) bool {
		return failingRuns[i].CreatedAt.Before(failingRuns[j].CreatedAt)
	})
	episodeSHA = failingRuns[0].HeadSHA

	return "red", episodeSHA, failingRuns
}

// splitRepoKey extracts owner and repo from an allowlist entry.
// Accepts both "host/owner/repo" (3 segments — the host is dropped, matches
// ParseRepoAllowlist output) and "owner/repo" (2 segments). Anything else
// returns the original key with an empty repo so the caller can skip it.
func splitRepoKey(key string) (owner, repo string) {
	parts := strings.Split(key, "/")
	switch len(parts) {
	case 3:
		return parts[1], parts[2]
	case 2:
		return parts[0], parts[1]
	default:
		return key, ""
	}
}

// buildCreateTaskCommand constructs a CreateTaskCommand for a build failure episode.
func (w *buildWatcher) buildCreateTaskCommand(
	ctx context.Context,
	taskID uuid.UUID,
	owner, repo, episodeSHA string,
	failingRuns []WorkflowRun,
	assignee, taskStatus, taskPhase string,
	includeLogs bool,
) task.CreateCommand {
	lines := w.buildBodyHeader(failingRuns[0], owner, repo)
	lines = append(lines,
		"",
		fmt.Sprintf("Episode SHA: `%s`", episodeSHA),
		"",
		"## Failing Workflows",
		"",
		"| Workflow | Job | Failed Step | Run |",
		"|---|---|---|---|",
	)

	var primaryJobID int64 // job ID for failingRuns[0] — used for log fetch
	for i, run := range failingRuns {
		select {
		case <-ctx.Done():
			return task.CreateCommand{}
		default:
		}
		jobName, stepName, jobID := w.fetchJobInfoForRun(ctx, owner, repo, run.RunID)
		if i == 0 {
			primaryJobID = jobID
		}
		lines = append(lines, fmt.Sprintf("| %s | %s | %s | [Run](%s) |",
			run.Name, jobName, stepName, run.HTMLURL))
	}

	if includeLogs && primaryJobID != 0 {
		lines = w.appendLogSection(ctx, lines, owner, repo, primaryJobID)
	}

	body := strings.Join(lines, "\n") + "\n"

	fm := agentlib.TaskFrontmatter{
		"task_type":   "build-fix",
		"assignee":    translateAssignee(assignee),
		"repo":        owner + "/" + repo,
		"episode_sha": episodeSHA,
		"status":      taskStatus,
	}
	if taskPhase != "" {
		fm["phase"] = taskPhase
	}
	return task.CreateCommand{
		Title: computeBuildTitle(
			"github",
			owner,
			repo,
			episodeSHA,
			w.maxTitleLen,
			w.taskSuffix,
		),
		TaskIdentifier: agentlib.TaskIdentifier(taskID.String()),
		Frontmatter:    fm,
		Body:           body,
	}
}

// buildBodyHeader builds the markdown header lines for a build-failure task body.
func (w *buildWatcher) buildBodyHeader(firstRun WorkflowRun, owner, repo string) []string {
	lines := make([]string, 0, 10)
	lines = append(
		lines,
		fmt.Sprintf("# Build Failure: [%s/%s](https://github.com/%s/%s)", owner, repo, owner, repo),
		"",
	)
	if firstRun.DisplayTitle != "" {
		lines = append(lines, fmt.Sprintf("**Commit:** %s", firstRun.DisplayTitle))
	}
	if firstRun.HeadBranch != "" {
		lines = append(lines, fmt.Sprintf("**Branch:** %s", firstRun.HeadBranch))
	}
	if firstRun.Event != "" {
		lines = append(lines, fmt.Sprintf("**Event:** %s", firstRun.Event))
	}
	if !firstRun.StartedAt.IsZero() {
		lines = append(
			lines,
			fmt.Sprintf("**Started:** %s", firstRun.StartedAt.UTC().Format(time.RFC3339)),
		)
	}
	if !firstRun.UpdatedAt.IsZero() {
		lines = append(
			lines,
			fmt.Sprintf("**Finished:** %s", firstRun.UpdatedAt.UTC().Format(time.RFC3339)),
		)
	}
	if !firstRun.StartedAt.IsZero() && !firstRun.UpdatedAt.IsZero() {
		if d := formatDuration(firstRun.UpdatedAt.Sub(firstRun.StartedAt)); d != "" {
			lines = append(lines, fmt.Sprintf("**Duration:** %s", d))
		}
	}
	return lines
}

// fetchJobInfoForRun returns job name, step name, and job ID for a failing run.
// Returns ("?", "?", 0) when the jobs API is unavailable or returns no failed jobs.
func (w *buildWatcher) fetchJobInfoForRun(
	ctx context.Context,
	owner, repo string,
	runID int64,
) (jobName, stepName string, jobID int64) {
	jobName, stepName = "?", "?"
	if runID == 0 {
		return
	}
	jobs, err := w.githubClient.GetJobsForRun(ctx, owner, repo, runID)
	if err != nil {
		glog.Warningf(
			"jobs API failed run=%d repo=%s/%s err=%v — using ? placeholders",
			runID,
			owner,
			repo,
			err,
		)
		return
	}
	if len(jobs) == 0 {
		return
	}
	jobName = jobs[0].JobName
	if jobs[0].FailedStepName != "" {
		stepName = jobs[0].FailedStepName
	}
	jobID = jobs[0].JobID
	return
}

// appendLogSection fetches the job log and appends an ## Error section to lines.
// Returns lines unchanged on any fetch error or when the log is empty.
func (w *buildWatcher) appendLogSection(
	ctx context.Context,
	lines []string,
	owner, repo string,
	jobID int64,
) []string {
	logData, err := w.githubClient.GetJobLog(ctx, owner, repo, jobID)
	if err != nil {
		glog.Warningf(
			"log fetch failed repo=%s/%s job=%d err=%v — omitting ## Error section",
			owner,
			repo,
			jobID,
			err,
		)
		return lines
	}
	if logData == nil {
		return lines
	}
	redacted := redactLogSnippet(string(logData))
	snippet := lastNLinesUpTo4KB(redacted, 30)
	if snippet == "" {
		return lines
	}
	return append(lines, "", "## Error", "", "```", snippet, "```")
}

// coalesceString returns the first non-empty string. Used to merge a
// per-repo file override (a) with the watcher-level default (b).
func coalesceString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// translateAssignee converts the magic string "human" to the empty string,
// which the operator-inbox convention treats as "unclaimed / needs human attention".
// Any other value is returned unchanged.
func translateAssignee(a string) string {
	if a == "human" {
		return ""
	}
	return a
}

// formatDuration formats d as a human-readable string for the task body header.
// Returns "" when d ≤ 0 so callers can omit the Duration line for zero timestamps.
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	d = d.Round(time.Second)
	if d <= 0 {
		return ""
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// redactLogSnippet applies regex redaction to remove known secret patterns from a
// CI log snippet before it enters the task body.
//
// ORDER MATTERS — apply specific patterns BEFORE the generic hex catch-all so token
// shapes that happen to be hex (e.g. github tokens are alphanumeric so unaffected;
// but a future bearer-token shape that's hex-only would be caught by step 5 with a
// generic [REDACTED] marker, losing the "Bearer " prefix). Reordering here is a bug.
//
// Pattern 5 (40+-char hex) WILL redact the episode SHA if it appears verbatim in
// log output. Acceptable: the SHA is already shown in plain text in the body header,
// so the operator hasn't lost recoverable context. False positives < leaked tokens.
// Compiled once at init — calling regexp.MustCompile inside redactLogSnippet
// recompiles the patterns on every invocation (the function runs once per
// failed-build episode). Hoisting saves the compile cost per call.
var (
	redactGitHubTokenRE  = regexp.MustCompile(`gh[opsu]_[a-zA-Z0-9]{16,}`)
	redactBearerAuthRE   = regexp.MustCompile(`Bearer\s+[A-Za-z0-9._-]{16,}`)
	redactAWSAccessKeyRE = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	// #nosec G101 — this pattern redacts user-provided AWS secret keys from CI logs, it is not a hardcoded credential
	redactAWSSecretKeyRE = regexp.MustCompile(
		`(aws_secret_access_key[\s=:]+["']?)[A-Za-z0-9/+]{40}["']?`,
	)
	redactOpaqueHexRE = regexp.MustCompile(`\b[a-f0-9]{40}\b`)
)

func redactLogSnippet(s string) string {
	// 1. GitHub tokens: gho_, ghp_, ghs_, ghu_ followed by ≥16 alphanumerics
	s = redactGitHubTokenRE.ReplaceAllString(s, "[REDACTED]")

	// 2. Bearer auth headers: "Bearer " followed by ≥16 token chars
	s = redactBearerAuthRE.ReplaceAllString(s, "Bearer [REDACTED]")

	// 3. AWS access key IDs: AKIA followed by 16 uppercase alphanumerics
	s = redactAWSAccessKeyRE.ReplaceAllString(s, "[REDACTED]")

	// 4. AWS secret access keys: keep the key= prefix, redact the 40-char base64 secret
	s = redactAWSSecretKeyRE.ReplaceAllString(s, "${1}[REDACTED]")

	// 5. SHA-1 hashes (exactly 40 hex chars) — generic auth hash catch-all.
	//    Runs last so specific patterns above (1-4) match their tokens first.
	s = redactOpaqueHexRE.ReplaceAllString(s, "[REDACTED]")

	return s
}

// lastNLinesUpTo4KB returns the last n lines of s, further capped at maxBytes bytes.
// Applied AFTER redaction to limit what enters the task body.
func lastNLinesUpTo4KB(s string, n int) string {
	const maxBytes = 4096
	// Trim trailing newline to avoid a phantom empty last line
	s = strings.TrimRight(s, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	snippet := strings.Join(lines, "\n")
	if len(snippet) > maxBytes {
		// Keep the tail: truncate from the start, then trim to the next line boundary
		snippet = snippet[len(snippet)-maxBytes:]
		if idx := strings.Index(snippet, "\n"); idx >= 0 && idx < len(snippet)-1 {
			snippet = snippet[idx+1:]
		}
	}
	return snippet
}
