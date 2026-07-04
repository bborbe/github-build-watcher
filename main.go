// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command maintainer-watcher-github-build polls GitHub Actions for failed
// workflow runs on the default branches of configured repos and publishes
// a CreateTaskCommand per green→red transition so a build-fixer agent can
// pick it up.
package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	"github.com/bborbe/github-build-watcher/pkg"
	"github.com/bborbe/github-build-watcher/pkg/auth"
	"github.com/bborbe/github-build-watcher/pkg/factory"
	"github.com/bborbe/github-build-watcher/pkg/filter"
	libhttp "github.com/bborbe/http"
	libkafka "github.com/bborbe/kafka"
	"github.com/bborbe/log"
	"github.com/bborbe/run"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/bborbe/maintainer/repoallowlist"
)

func validateMaxTitleLen(ctx context.Context, maxTitleLen int) error {
	if maxTitleLen <= 0 {
		return errors.Errorf(ctx, "MAX_TITLE_LEN must be > 0; got %d", maxTitleLen)
	}
	return nil
}

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"SentryDSN"    display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy"`

	Listen         string           `required:"false" arg:"listen"          env:"LISTEN"          usage:"HTTP listen address (healthz/readiness/metrics/trigger)"                               default:":9090"`
	AppID          int64            `required:"false" arg:"app-id"          env:"APP_ID"          usage:"GitHub App ID (numeric); required for App auth"`
	InstallationID int64            `required:"false" arg:"installation-id" env:"INSTALLATION_ID" usage:"GitHub App Installation ID (numeric)"`
	PEMKeyFile     string           `required:"false" arg:"pem-key-file"    env:"PEM_KEY_FILE"    usage:"Path to the GitHub App private key (PEM) mounted from k8s Secret"`
	PEMKey         string           `required:"false" arg:"pem-key"         env:"PEM_KEY"         usage:"GitHub App private key (PEM) as env var content; mutually exclusive with PEM_KEY_FILE"                 display:"length"`
	KafkaBrokers   libkafka.Brokers `required:"true"  arg:"kafka-brokers"   env:"KAFKA_BROKERS"   usage:"Comma-separated Kafka broker list"`
	Stage          string           `required:"true"  arg:"stage"           env:"STAGE"           usage:"Deployment stage (dev|prod)"`
	PollInterval   string           `required:"false" arg:"poll-interval"   env:"POLL_INTERVAL"   usage:"Poll interval (Go duration)"                                                           default:"5m"`
	RepoAllowlist  string           `required:"true"  arg:"repo-allowlist"  env:"REPO_ALLOWLIST"  usage:"Comma-separated host-qualified repo allowlist (host/owner/repo); MUST be non-empty"`

	BuildAssignee   string `required:"true"  arg:"build-assignee"    env:"TASK_ASSIGNEE" usage:"Frontmatter assignee for published tasks"                                                                                                                                                                             default:"build-fixer-agent"`
	BuildTaskStatus string `required:"true"  arg:"build-task-status" env:"TASK_STATUS"   usage:"Frontmatter status for published tasks"                                                                                                                                                                               default:"next"`
	BuildTaskPhase  string `required:"false" arg:"build-task-phase"  env:"TASK_PHASE"    usage:"Frontmatter phase for published tasks; empty = omit field"`
	MaxTitleLen     int    `required:"false" arg:"max-title-len"     env:"MAX_TITLE_LEN" usage:"Max length of vault task filename (whole title; safety cap)"                                                                                                                                                          default:"200"`
	TaskSuffix      string `required:"false" arg:"task-suffix"       env:"TASK_SUFFIX"   usage:"Optional suffix appended to build-failure task filenames as ' - suffix'; empty = no suffix. Use distinct values per stage to prevent task-file collisions when both watchers poll the same repo into the same vault."`

	// TopicPrefix selects the Kafka topic prefix used for CQRS topic construction
	// (e.g. "develop" / "master"); independent of Stage, which remains the
	// deployment-stage identifier used for image tags and other non-topic
	// purposes. Empty means unprefixed topics.
	TopicPrefix base.TopicPrefix `required:"false" arg:"topic-prefix" env:"TOPIC_PREFIX" usage:"Kafka topic prefix for CQRS topic construction"`

	TriggerHandler http.Handler
}

//nolint:funlen // wires Run from validated config — extracting any chunk hurts readability without reducing complexity. 90+ lines, over the 80-line cap.
func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	if err := validateMaxTitleLen(ctx, a.MaxTitleLen); err != nil {
		return err
	}

	pollInterval, err := time.ParseDuration(a.PollInterval)
	if err != nil {
		return errors.Wrapf(ctx, err, "parse poll interval %q", a.PollInterval)
	}

	repoAllowlist, err := filter.ParseRepoAllowlist(a.RepoAllowlist)
	if err != nil {
		return errors.Wrap(ctx, err, "parse repo allowlist")
	}
	// Validate ALL entries at startup — aggregate error names every malformed entry.
	if validationErr := repoallowlist.Validate(ctx, repoAllowlist); validationErr != nil {
		return errors.Wrap(ctx, validationErr, "REPO_ALLOWLIST contains malformed entries")
	}
	if len(repoAllowlist) == 0 {
		return errors.Errorf(
			ctx,
			"REPO_ALLOWLIST must be non-empty: set at least one host/owner/repo entry",
		)
	}
	glog.V(2).Infof("repo-allowlist count=%d", len(repoAllowlist))

	httpClient, err := auth.Resolve(ctx, auth.Config{
		AppID:          a.AppID,
		InstallationID: a.InstallationID,
		PEMKeyFile:     a.PEMKeyFile,
		PEMKey:         a.PEMKey,
		LogPrefix:      "watcher/github-build",
	})
	if err != nil {
		return errors.Wrap(ctx, err, "resolve auth")
	}
	defer httpClient.CloseIdleConnections()

	ghClient := pkg.NewGitHubClient(httpClient)

	resolved, refreshTask, err := factory.CreateAllowlistSnapshot(ghClient, repoAllowlist)
	if err != nil {
		return errors.Wrap(ctx, err, "create allowlist snapshot")
	}

	currentDateTime := libtime.NewCurrentDateTime()
	w, syncProducer, watcherCleanup, err := factory.CreateWatcher(
		ctx,
		ghClient,
		a.KafkaBrokers,
		a.TopicPrefix,
		repoAllowlist,
		resolved,
		"/data/cursor.json",
		a.BuildAssignee,
		a.BuildTaskStatus,
		a.BuildTaskPhase,
		a.MaxTitleLen,
		a.TaskSuffix,
		currentDateTime, // spec 069: clock for force=true salt nonce
	)
	if err != nil {
		return errors.Wrap(ctx, err, "create watcher")
	}
	defer watcherCleanup()

	// HTTP-side sender backs the /trigger handler.
	triggerBuildCheckSender := factory.CreateTriggerBuildCheckCommandSender(
		ctx,
		syncProducer,
		a.TopicPrefix,
	)
	triggerHandler := factory.CreateTriggerBuildCheckHandler(triggerBuildCheckSender)
	a.TriggerHandler = libhttp.NewJSONErrorHandler(triggerHandler)

	// In-pod command consumer: third run.Func alongside poll + HTTP.
	// session-scoped offset store — replays the request topic from OffsetOldest
	// on pod restart; safe because the downstream Watcher.Poll is idempotent
	// via the per-(repo,run) cursor + per-task derived task_id.
	saramaClientProvider := libkafka.NewSaramaClientProviderNew(a.KafkaBrokers)
	db := pkg.NewMemDB()
	commandConsumer := factory.CreateCommandConsumer(
		saramaClientProvider,
		syncProducer,
		db,
		w, // shared with the poll-interval loop
		a.TopicPrefix,
	)

	glog.V(2).
		Infof("maintainer-watcher-github-build starting stage=%s interval=%s listen=%s", a.Stage, a.PollInterval, a.Listen)

	pollOnce := a.pollOnce(w)

	// Order: poll → HTTP → command consumer (spec 068 AC 9: three run.Funcs).
	tasks := []run.Func{
		a.runPollLoop(pollOnce, pollInterval),
		a.createHTTPServer(),
		commandConsumer,
	}
	if refreshTask != nil {
		tasks = append(tasks, refreshTask)
	}
	return run.CancelOnFirstFinish(ctx, tasks...)
}

func (a *application) pollOnce(w pkg.Watcher) run.Func {
	return func(ctx context.Context) error {
		glog.V(2).Infof("poll cycle start stage=%s", a.Stage)
		return w.Poll(ctx, false)
	}
}

func (a *application) runPollLoop(
	poll run.Func,
	interval time.Duration,
) run.Func {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Fire one cycle immediately on start, then on each tick.
		if err := poll(ctx); err != nil {
			glog.Errorf("initial poll: %v", err)
		}
		for {
			select {
			case <-ctx.Done():
				glog.V(2).Infof("poll loop: context cancelled, exiting cleanly")
				return nil
			case <-ticker.C:
				if err := poll(ctx); err != nil {
					glog.Errorf("poll cycle error: %v", err)
				}
			}
		}
	}
}

func (a *application) createHTTPServer() run.Func {
	return func(ctx context.Context) error {
		router := mux.NewRouter()
		router.Path("/healthz").Handler(libhttp.NewPrintHandler("OK"))
		router.Path("/readiness").Handler(libhttp.NewPrintHandler("OK"))
		router.Path("/metrics").Handler(promhttp.Handler())
		router.Path("/setloglevel/{level}").
			Handler(log.NewSetLoglevelHandler(ctx, log.NewLogLevelSetter(2, 5*time.Minute)))
		router.Path("/resetcursor/{repo:.+}").
			Handler(libhttp.NewDangerousHandlerWrapper(pkg.NewResetCursorHandler(pkg.DefaultCursorPath)))
		router.Path("/trigger").Handler(a.TriggerHandler)
		glog.V(2).Infof("http server listening on %s", a.Listen)
		return libhttp.NewServer(a.Listen, router).Run(ctx)
	}
}
