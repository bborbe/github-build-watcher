// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maintenance_test

import (
	"context"
	stderrors "errors"

	"github.com/bborbe/github-build-watcher/mocks"
	"github.com/bborbe/github-build-watcher/pkg"
	"github.com/bborbe/github-build-watcher/pkg/maintenance"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Loader", func() {
	var (
		ctx     context.Context
		fetcher *mocks.FileContentFetcher
		loader  maintenance.Loader
	)

	BeforeEach(func() {
		ctx = context.Background()
		fetcher = new(mocks.FileContentFetcher)
		loader = maintenance.NewLoader(fetcher)
	})

	Context("file not found (404)", func() {
		It("returns empty config silently", func() {
			fetcher.GetFileContentReturns(nil, nil)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg).To(Equal(maintenance.GithubBuildConfig{}))
			Expect(fetcher.GetFileContentCallCount()).To(Equal(1))
		})
	})

	Context("API error (5xx or other)", func() {
		It("returns empty config and does not panic", func() {
			fetcher.GetFileContentReturns(nil, stderrors.New("http 500 internal server error"))
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg).To(Equal(maintenance.GithubBuildConfig{}))
		})
	})

	Context("file too large (returned as error by GetFileContent)", func() {
		It("returns empty config", func() {
			fetcher.GetFileContentReturns(
				nil,
				stderrors.New(
					"file owner/repo/.maintenance.yaml too large: 1048577 bytes (max 1 MiB)",
				),
			)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg).To(Equal(maintenance.GithubBuildConfig{}))
		})
	})

	Context("malformed YAML", func() {
		It("returns empty config", func() {
			fetcher.GetFileContentReturns(
				[]byte("watcher:\n  github-build:\n    assignee: [invalid yaml"),
				nil,
			)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg).To(Equal(maintenance.GithubBuildConfig{}))
		})
	})

	Context("valid YAML — no watcher.github-build subtree", func() {
		It("returns empty config (subtree isolation)", func() {
			content := []byte(`watcher:
  github-pr:
    assignee: pr-reviewer-agent
`)
			fetcher.GetFileContentReturns(content, nil)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg).To(Equal(maintenance.GithubBuildConfig{}))
		})
	})

	Context("valid YAML — all three keys set", func() {
		It("returns all three overrides", func() {
			content := []byte(`watcher:
  github-build:
    assignee: go-deps-fixer-agent
    status: backlog
    phase: planning
`)
			fetcher.GetFileContentReturns(content, nil)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg.Assignee).To(Equal("go-deps-fixer-agent"))
			Expect(cfg.Status).To(Equal("backlog"))
			Expect(cfg.Phase).To(Equal("planning"))
		})
	})

	Context("valid YAML — only assignee set", func() {
		It("returns assignee override; status and phase are empty string", func() {
			content := []byte(`watcher:
  github-build:
    assignee: go-deps-fixer-agent
`)
			fetcher.GetFileContentReturns(content, nil)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg.Assignee).To(Equal("go-deps-fixer-agent"))
			Expect(cfg.Status).To(Equal(""))
			Expect(cfg.Phase).To(Equal(""))
		})
	})

	Context("valid YAML — assignee is empty string", func() {
		It("treats empty string as absent (returns empty Assignee)", func() {
			content := []byte(`watcher:
  github-build:
    assignee: ""
`)
			fetcher.GetFileContentReturns(content, nil)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg.Assignee).To(Equal(""))
		})
	})

	Context("valid YAML — unknown key in watcher.github-build", func() {
		It("applies known keys and ignores unknown ones without error", func() {
			content := []byte(`watcher:
  github-build:
    assignee: go-deps-fixer-agent
    priority: high
`)
			fetcher.GetFileContentReturns(content, nil)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg.Assignee).To(Equal("go-deps-fixer-agent"))
		})
	})

	Context("fetch passes the correct ref to GetFileContent", func() {
		It("uses the defaultBranch as the ref", func() {
			fetcher.GetFileContentReturns(nil, nil)
			loader.LoadOverrides(ctx, "myorg", "myrepo", "develop")
			_, calledOwner, calledRepo, calledPath, calledRef := fetcher.GetFileContentArgsForCall(
				0,
			)
			Expect(calledOwner).To(Equal("myorg"))
			Expect(calledRepo).To(Equal("myrepo"))
			Expect(calledPath).To(Equal(".maintenance.yaml"))
			Expect(calledRef).To(Equal("develop"))
		})
	})

	Context("GitHub fetch returns ErrRateLimited", func() {
		It("returns empty config without erroring (caller continues with defaults)", func() {
			fetcher.GetFileContentReturns(nil, pkg.ErrRateLimited)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg.Assignee).To(BeEmpty())
			Expect(cfg.Status).To(BeEmpty())
			Expect(cfg.Phase).To(BeEmpty())
		})
	})

	Context("valid YAML — include_logs: true", func() {
		It("returns IncludeLogs=true", func() {
			content := []byte(`watcher:
  github-build:
    assignee: go-deps-fixer-agent
    include_logs: true
`)
			fetcher.GetFileContentReturns(content, nil)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg.IncludeLogs).To(BeTrue())
			Expect(cfg.Assignee).To(Equal("go-deps-fixer-agent"))
		})
	})

	Context("valid YAML — include_logs absent (default false)", func() {
		It("returns IncludeLogs=false", func() {
			content := []byte(`watcher:
  github-build:
    assignee: build-fixer-agent
`)
			fetcher.GetFileContentReturns(content, nil)
			cfg := loader.LoadOverrides(ctx, "owner", "repo", "main")
			Expect(cfg.IncludeLogs).To(BeFalse())
		})
	})
})
