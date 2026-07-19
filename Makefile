include Makefile.variables
include Makefile.precommit
include Makefile.docker

SERVICE = github-build-watcher

.PHONY: fix
fix:
	@for dir in $$(find `pwd` -type d -name vendor -prune -o -name go.mod -exec dirname "{}" \; | grep -v '^$$'); do \
		cd $${dir}; \
		echo "fix $${dir}"; \
		go get \
		github.com/IBM/sarama@latest \
		github.com/bborbe/agent/lib@latest \
		github.com/bborbe/argument/v2@latest \
		github.com/bborbe/badgerkv@latest \
		github.com/bborbe/boltkv@latest \
		github.com/bborbe/kv@latest \
		github.com/bborbe/memorykv@latest \
		github.com/containerd/containerd@latest \
		github.com/go-git/go-git/v5@latest \
		golang.org/x/crypto@latest \
		golang.org/x/net@latest; \
	done
