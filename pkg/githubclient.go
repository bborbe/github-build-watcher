// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	stderrors "errors"
	"io"
	"net/http"
	"time"

	"github.com/bborbe/errors"
	gogithub "github.com/google/go-github/v62/github"
)

// ErrRateLimited is returned when the GitHub API responds with a rate-limit or
// abuse-rate-limit error.
var ErrRateLimited = stderrors.New("github rate limited")

// WorkflowRun holds the fields the watcher needs from a GitHub Actions workflow run.
type WorkflowRun struct {
	WorkflowID   int64
	RunID        int64 // run instance ID — used by jobs API (GET /actions/runs/{id}/jobs)
	Name         string
	HeadSHA      string
	Conclusion   string
	HTMLURL      string
	CreatedAt    time.Time
	DisplayTitle string    // display_title: commit message shown in GitHub UI
	HeadBranch   string    // head_branch: branch that triggered the run
	Event        string    // event: push / pull_request / schedule / workflow_dispatch / etc.
	StartedAt    time.Time // run_started_at: when execution began (not queuing time)
	UpdatedAt    time.Time // updated_at: last status change — completion time for done runs
}

// WorkflowJobInfo holds the failed job and step names for one failing workflow run.
// If no failed job is found in the response, JobName and FailedStepName are empty strings.
type WorkflowJobInfo struct {
	JobID          int64
	JobName        string
	FailedStepName string // first failed step's name; empty when not determinable
}

//counterfeiter:generate -o ../mocks/github_client.go --fake-name GitHubClient . GitHubClient

// GitHubClient abstracts the GitHub Actions API calls.
type GitHubClient interface {
	// GetWorkflowRuns returns completed workflow runs for a repo branch.
	// In-progress runs (empty Conclusion) are filtered out.
	GetWorkflowRuns(ctx context.Context, owner, repo, branch string) ([]WorkflowRun, error)

	// GetDefaultBranch returns the default branch name for a repository.
	GetDefaultBranch(ctx context.Context, owner, repo string) (string, error)

	// GetFileContent fetches the raw content of a file at the given ref.
	// Returns (nil, nil) if the file does not exist (HTTP 404 — the common case).
	// Returns (nil, ErrRateLimited) when rate-limited.
	// Returns (nil, err) for any other API error.
	GetFileContent(ctx context.Context, owner, repo, path, ref string) ([]byte, error)

	// GetJobsForRun returns info about failed jobs in a run.
	// Returns an empty slice (not an error) when the run has no failed jobs.
	// Returns (nil, ErrRateLimited) when rate-limited.
	// Returns (nil, err) for other API errors.
	GetJobsForRun(ctx context.Context, owner, repo string, runID int64) ([]WorkflowJobInfo, error)

	// GetJobLog fetches the plain-text log for a workflow job by following GitHub's
	// redirect to an Azure storage URL. Returns (nil, nil) when the URL is unavailable.
	// Returns (nil, err) for a non-nil error where the log should be omitted.
	// Rejects payloads > 1 MiB before truncation (returns (nil, err)).
	GetJobLog(ctx context.Context, owner, repo string, jobID int64) ([]byte, error)

	// ListOwnerRepos returns the names of every non-archived, non-fork
	// repository owned by `owner`. Owner kind (User vs Organization) is
	// detected via GET /users/<owner>; the method then calls ListByUser
	// or ListByOrg respectively, paginating with PerPage=100 until done.
	// Returns (nil, ErrRateLimited) when rate-limited.
	// Returns (nil, err) for any other API error (network, 401/403, 404).
	// Returns ([]string{}, nil) when the owner has zero eligible repos.
	ListOwnerRepos(ctx context.Context, owner string) ([]string, error)
}

// NewGitHubClient returns a GitHubClient backed by the real GitHub API.
func NewGitHubClient(httpClient *http.Client) GitHubClient {
	return &githubClient{
		client: gogithub.NewClient(httpClient),
	}
}

type githubClient struct {
	client *gogithub.Client
}

func (c *githubClient) GetWorkflowRuns(
	ctx context.Context,
	owner, repo, branch string,
) ([]WorkflowRun, error) {
	opts := &gogithub.ListWorkflowRunsOptions{
		Branch: branch,
		Status: "completed",
		ListOptions: gogithub.ListOptions{
			PerPage: 20,
		},
	}
	result, _, err := c.client.Actions.ListRepositoryWorkflowRuns(ctx, owner, repo, opts)
	if err != nil {
		var rl *gogithub.RateLimitError
		var arl *gogithub.AbuseRateLimitError
		if stderrors.As(err, &rl) || stderrors.As(err, &arl) {
			return nil, ErrRateLimited
		}
		return nil, errors.Wrapf(
			ctx,
			err,
			"list workflow runs %s/%s branch=%s",
			owner,
			repo,
			branch,
		)
	}

	runs := make([]WorkflowRun, 0, len(result.WorkflowRuns))
	for _, run := range result.WorkflowRuns {
		if run.GetConclusion() == "" {
			continue
		}
		var createdAt time.Time
		if run.CreatedAt != nil {
			createdAt = run.CreatedAt.Time
		}
		runs = append(runs, WorkflowRun{
			WorkflowID:   run.GetWorkflowID(),
			RunID:        run.GetID(),
			Name:         run.GetName(),
			HeadSHA:      run.GetHeadSHA(),
			Conclusion:   run.GetConclusion(),
			HTMLURL:      run.GetHTMLURL(),
			CreatedAt:    createdAt,
			DisplayTitle: run.GetDisplayTitle(),
			HeadBranch:   run.GetHeadBranch(),
			Event:        run.GetEvent(),
			StartedAt:    run.GetRunStartedAt().Time,
			UpdatedAt:    run.GetUpdatedAt().Time,
		})
	}
	return runs, nil
}

func (c *githubClient) GetDefaultBranch(
	ctx context.Context,
	owner, repo string,
) (string, error) {
	repository, _, err := c.client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		var rl *gogithub.RateLimitError
		var arl *gogithub.AbuseRateLimitError
		if stderrors.As(err, &rl) || stderrors.As(err, &arl) {
			return "", ErrRateLimited
		}
		return "", errors.Wrapf(ctx, err, "get repository %s/%s", owner, repo)
	}
	return repository.GetDefaultBranch(), nil
}

func (c *githubClient) GetFileContent(
	ctx context.Context,
	owner, repo, path, ref string,
) ([]byte, error) {
	opts := &gogithub.RepositoryContentGetOptions{Ref: ref}
	fileContent, _, _, err := c.client.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		var ghErr *gogithub.ErrorResponse
		if stderrors.As(err, &ghErr) && ghErr.Response.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		var rl *gogithub.RateLimitError
		var arl *gogithub.AbuseRateLimitError
		if stderrors.As(err, &rl) || stderrors.As(err, &arl) {
			return nil, ErrRateLimited
		}
		return nil, errors.Wrapf(ctx, err, "get file content %s/%s/%s@%s", owner, repo, path, ref)
	}
	if fileContent == nil {
		return nil, nil
	}
	if fileContent.GetSize() > 1024*1024 {
		return nil, errors.Errorf(
			ctx,
			"file %s/%s/%s too large: %d bytes (max 1 MiB)",
			owner,
			repo,
			path,
			fileContent.GetSize(),
		)
	}
	decoded, err := fileContent.GetContent()
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "decode content %s/%s/%s", owner, repo, path)
	}
	return []byte(decoded), nil
}

func (c *githubClient) GetJobsForRun(
	ctx context.Context,
	owner, repo string,
	runID int64,
) ([]WorkflowJobInfo, error) {
	opts := &gogithub.ListWorkflowJobsOptions{Filter: "latest"}
	result, _, err := c.client.Actions.ListWorkflowJobs(ctx, owner, repo, runID, opts)
	if err != nil {
		var rl *gogithub.RateLimitError
		var arl *gogithub.AbuseRateLimitError
		if stderrors.As(err, &rl) || stderrors.As(err, &arl) {
			return nil, ErrRateLimited
		}
		return nil, errors.Wrapf(
			ctx,
			err,
			"list jobs for run %d owner=%s repo=%s",
			runID,
			owner,
			repo,
		)
	}
	var infos []WorkflowJobInfo
	for _, job := range result.Jobs {
		if job.GetConclusion() != "failure" {
			continue
		}
		var failedStep string
		for _, step := range job.Steps {
			if step.GetConclusion() == "failure" {
				failedStep = step.GetName()
				break
			}
		}
		infos = append(infos, WorkflowJobInfo{
			JobID:          job.GetID(),
			JobName:        job.GetName(),
			FailedStepName: failedStep,
		})
	}
	return infos, nil
}

func (c *githubClient) GetJobLog(
	ctx context.Context,
	owner, repo string,
	jobID int64,
) ([]byte, error) {
	logURL, _, err := c.client.Actions.GetWorkflowJobLogs(ctx, owner, repo, jobID, 0)
	if err != nil {
		var rl *gogithub.RateLimitError
		var arl *gogithub.AbuseRateLimitError
		if stderrors.As(err, &rl) || stderrors.As(err, &arl) {
			return nil, ErrRateLimited
		}
		return nil, errors.Wrapf(
			ctx,
			err,
			"get job log URL owner=%s repo=%s job=%d",
			owner,
			repo,
			jobID,
		)
	}
	if logURL == nil {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, logURL.String(), nil)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "create log request job=%d", jobID)
	}
	resp, err := http.DefaultClient.Do(
		req,
	) // #nosec G107 — URL comes from GitHub API, not user input
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "fetch log job=%d", jobID)
	}
	defer resp.Body.Close()

	const maxBytes = 1024 * 1024 // 1 MiB
	// Read one extra byte to detect >1 MiB without reading the entire payload:
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "read log body job=%d", jobID)
	}
	if len(data) > maxBytes {
		return nil, errors.Errorf(
			ctx,
			"log payload exceeds 1 MiB for job=%d (got %d bytes) — treating as suspicious",
			jobID,
			len(data),
		)
	}
	return data, nil
}

func (c *githubClient) ListOwnerRepos(ctx context.Context, owner string) ([]string, error) {
	user, _, err := c.client.Users.Get(ctx, owner)
	if err != nil {
		return nil, c.wrapRateLimitErr(ctx, err, "get user %s", owner)
	}

	isOrg := user.GetType() == "Organization"
	return c.listOwnerReposPaginated(ctx, owner, isOrg)
}

func (c *githubClient) listOwnerReposPaginated(
	ctx context.Context,
	owner string,
	isOrg bool,
) ([]string, error) {
	names := make([]string, 0, 32)
	page := 1
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		repos, resp, err := c.fetchRepoPage(ctx, owner, isOrg, page)
		if err != nil {
			return nil, c.wrapRateLimitErr(ctx, err, "list repos for %s page=%d", owner, page)
		}
		names = append(names, filterRepoNames(repos)...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return names, nil
}

func (c *githubClient) fetchRepoPage(
	ctx context.Context,
	owner string,
	isOrg bool,
	page int,
) ([]*gogithub.Repository, *gogithub.Response, error) {
	if isOrg {
		opts := &gogithub.RepositoryListByOrgOptions{
			ListOptions: gogithub.ListOptions{PerPage: 100, Page: page},
		}
		return c.client.Repositories.ListByOrg(ctx, owner, opts)
	}
	opts := &gogithub.RepositoryListByUserOptions{
		ListOptions: gogithub.ListOptions{PerPage: 100, Page: page},
	}
	return c.client.Repositories.ListByUser(ctx, owner, opts)
}

func (c *githubClient) wrapRateLimitErr(
	ctx context.Context,
	err error,
	msg string,
	args ...interface{},
) error {
	var rl *gogithub.RateLimitError
	var arl *gogithub.AbuseRateLimitError
	if stderrors.As(err, &rl) || stderrors.As(err, &arl) {
		return ErrRateLimited
	}
	return errors.Wrapf(ctx, err, msg, args...)
}

func filterRepoNames(repos []*gogithub.Repository) []string {
	var names []string
	for _, repo := range repos {
		if repo.GetArchived() || repo.GetFork() {
			continue
		}
		name := repo.GetName()
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}
