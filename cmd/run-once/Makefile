include ../../../../Makefile.variables

# Override on the command line:
#   make run-once REPO_ALLOWLIST=github.com/bborbe/some-repo
# Default targets bborbe/maintainer because its CI is currently red and
# produces an observable end-to-end publish on smoke tests.
REPO_ALLOWLIST ?= github.com/bborbe/maintainer
STAGE          ?= dev
TASK_ASSIGNEE  ?= build-fixer-agent
TASK_STATUS    ?= todo
TASK_PHASE     ?=

run-once:
	@GH_TOKEN=$$(gh auth token) go run -mod=mod . \
		-kafka-brokers="$(KAFKA_BROKERS)" \
		-stage=$(STAGE) \
		-repo-allowlist="$(REPO_ALLOWLIST)" \
		-build-assignee="$(TASK_ASSIGNEE)" \
		-build-task-status="$(TASK_STATUS)" \
		-build-task-phase="$(TASK_PHASE)" \
		-v=2

# run-once/ is a CLI utility, not a deployable service. The root Makefile's
# `buca`/`apply` targets recurse into every subdir; these no-ops satisfy the
# recursion without producing a Docker image or k8s rollout.
.PHONY: buca apply
buca:
	@echo "run-once/ is a CLI utility — no Docker image to build, skipping"

apply:
	@echo "run-once/ is a CLI utility — no k8s manifests to apply, skipping"
