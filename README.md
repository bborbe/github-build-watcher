# github-build-watcher

Polls the GitHub Actions API for failed CI workflow runs on the default branches of a configured
repo allowlist and publishes a `CreateTaskCommand` to Kafka on each `green → red` transition so a
build-fixer agent can pick it up.

## Links

Dev:
https://dev.quant.benjamin-borbe.de/admin/maintainer-watcher-github-build/setloglevel/3
https://dev.quant.benjamin-borbe.de/admin/maintainer-watcher-github-build/trigger

Prod:
https://prod.quant.benjamin-borbe.de/admin/maintainer-watcher-github-build/setloglevel/3
https://prod.quant.benjamin-borbe.de/admin/maintainer-watcher-github-build/trigger

## How It Works

For each repo in the allowlist the watcher fetches the latest completed workflow runs on the
default branch, deduplicates by `workflow_id` (latest run per workflow), and derives a per-repo
state of `green` or `red`. State transitions drive Kafka publishing:

| Previous → Current | Action |
|---|---|
| `green` (or cold start) → `red` | Publish a task with deterministic `UUID5("<owner>/<repo>#build-<episode-sha>")` |
| `red` → `red` (any SHA) | Skip — the episode is locked on the first red SHA |
| `red` → `green` | Clear cursor state; no closure published (deferred to follow-up spec) |
| `green` → `green` | Nothing |
| any → undefined (zero completed runs) | Skip |

Re-polls of the same broken commit always produce the same task ID (the controller deduplicates),
so re-deploys and pod restarts are safe. A new red episode on a different commit produces a
distinct task ID.

See [`docs/build-watcher.md`](https://github.com/bborbe/maintainer/blob/master/docs/build-watcher.md) for episode-SHA semantics, derivation
rules, the worked example (t0–t4), and cold-start flood behaviour.

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `GH_TOKEN` | yes | — | GitHub personal access token (read scope sufficient) |
| `KAFKA_BROKERS` | yes | — | Comma-separated Kafka broker list |
| `STAGE` | yes | — | Deployment stage (`dev` or `prod`) |
| `REPO_ALLOWLIST` | yes | — | Comma-separated host-qualified repo allowlist (`host/owner/repo`); MUST be non-empty |
| `LISTEN` | no | `:9090` | HTTP listen address (`/healthz`, `/readiness`, `/metrics`, `/trigger`) |
| `POLL_INTERVAL` | no | `5m` | Poll interval (Go duration string) |
| `SENTRY_DSN` | no | — | Sentry DSN for error tracking |
| `SENTRY_PROXY` | no | — | HTTP proxy URL for Sentry transport |
| `TASK_ASSIGNEE` | no | `build-fixer-agent` | Frontmatter `assignee` written to published tasks; explicit empty string rejected at startup |
| `TASK_STATUS` | no | `todo` | Frontmatter `status` written to published tasks; explicit empty string rejected at startup |
| `TASK_PHASE` | no | (empty — omitted) | Frontmatter `phase` written to published tasks; if empty or unset, the key is NOT written to frontmatter |

### `REPO_ALLOWLIST` syntax

Entries are comma-separated. A leading `!` marks an exclusion. A target is allowed iff `(includes is empty OR any include matches) AND (no exclude matches)`; excludes always override includes.

| Entry shape | Example | Meaning |
|---|---|---|
| Literal include | `github.com/bborbe/maintainer` | Allow exactly this repo |
| Wildcard include | `github.com/bborbe/*` | Allow every repo under this owner |
| Literal exclude | `!github.com/bborbe/go-skeleton` | Reject exactly this repo (overrides any matching include) |
| Wildcard exclude | `!github.com/bborbe/*` | Reject every repo under this owner |

An allowlist consisting of only exclude entries is treated as allow-all-except: every target passes the include gate, and only the exclude gate filters. Example: `REPO_ALLOWLIST=!github.com/bborbe/go-skeleton` rejects go-skeleton and allows every other repo (including all other bborbe repos). To allow every bborbe repo except go-skeleton, write `github.com/bborbe/*,!github.com/bborbe/go-skeleton`.

The container env vars above are short and pod-scoped. The deploy-side env file (`dev.env` / `prod.env`) uses long, namespaced names (`WATCHER_GITHUB_BUILD_TASK_ASSIGNEE` etc.) because that file holds variables for multiple services; the StatefulSet template (`k8s/maintainer-watcher-github-build-sts.yaml`) maps long → short on its way into the pod.

## HTTP Endpoints

| Path | Purpose |
|---|---|
| `/healthz` | Liveness probe |
| `/readiness` | Readiness probe |
| `/metrics` | Prometheus metrics |
| `/trigger` | Run a poll cycle in the background; returns 200 immediately |

## Prometheus Metrics

| Metric | Type | Labels | Purpose |
|---|---|---|---|
| `github_build_watcher_poll_cycles_total` | counter | `result` (`success` \| `error`) | Total poll cycles |
| `github_build_watcher_repos_checked_total` | counter | — | Repos successfully fetched |
| `github_build_watcher_state_transitions_total` | counter | `transition` (`green_to_red` \| `red_to_green`) | Per-repo state transitions |
| `github_build_watcher_tasks_published_total` | counter | — | `CreateTaskCommand` messages published to Kafka |
| `github_build_watcher_poll_errors_total` | counter | `reason` (`rate_limited` \| `github_error` \| `kafka_error`) | Poll errors by reason |
| `github_build_watcher_current_red_repos` | gauge | — | Repos currently in `red` state |

## Development

```bash
make test          # run unit tests
make generate      # regenerate counterfeiter mocks
make precommit     # format + lint + test + security checks
```

### Local one-shot runner

`cmd/run-once` runs a single poll cycle then exits — useful for smoke testing against a real repo
without standing up the full HTTP server / poll loop:

```bash
go run ./cmd/run-once \
  --gh-token=$GH_TOKEN \
  --kafka-brokers=localhost:9092 \
  --stage=dev \
  --repo-allowlist=github.com/bborbe/go-skeleton
```

## Cursor Mechanism

The cursor at `/data/cursor.json` records, per repo: `last_known_state` (`green` \| `red`),
`current_episode_sha` (the SHA that anchors the active red episode), and the cached
`default_branch`. The Kafka publish failure invariant: if `PublishCreate` returns an error, the
cursor is NOT updated for that repo so the next poll retries (same task ID — controller dedups).

A corrupt cursor causes `Poll` to return the load error and skip the cycle; the next poll retries.

## Relationship to build-fixer-agent

Tasks are emitted with `assignee: build-fixer-agent`. The build-fixer agent (a planned follow-up
in `specs/ideas/build-fixer-agent.md`) clones the repo at the episode SHA, classifies the failure
from the workflow logs, and dispatches a matching dark-factory runbook (e.g. `go-deps-update`).

See [`docs/architecture.md`](https://github.com/bborbe/maintainer/blob/master/docs/architecture.md) for the full pipeline.

## License

BSD 2-Clause License. See [LICENSE](LICENSE).
