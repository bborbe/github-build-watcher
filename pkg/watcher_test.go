// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"time"

	taskmocks "github.com/bborbe/agent/mocks"
	"github.com/bborbe/github-build-watcher/mocks"
	"github.com/bborbe/github-build-watcher/pkg"
	"github.com/bborbe/github-build-watcher/pkg/filter"
	"github.com/bborbe/github-build-watcher/pkg/maintenance"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Watcher", func() {
	var ctx context.Context
	var ghClient *mocks.GitHubClient
	var createSender *taskmocks.TaskCreateCommandSender
	var metrics *mocks.Metrics
	var tmpDir string
	var cursorPath string

	BeforeEach(func() {
		ctx = context.Background()
		tmpDir = GinkgoT().TempDir()
		cursorPath = filepath.Join(tmpDir, "cursor.json")
		ghClient = new(mocks.GitHubClient)
		createSender = new(taskmocks.TaskCreateCommandSender)
		metrics = new(mocks.Metrics)
	})

	makeWatcher := func(allowlist []string) pkg.Watcher {
		ml := new(mocks.MaintenanceLoader)
		ml.LoadOverridesReturns(maintenance.GithubBuildConfig{})
		return pkg.NewWatcher(
			ghClient,
			createSender,
			metrics,
			filter.RepoFilters{},
			pkg.NewStaticSnapshot(allowlist),
			cursorPath,
			"build-fixer-agent",
			"todo",
			"",
			ml,
			pkg.DefaultMaxTitleLen,
			"",
			libtime.NewCurrentDateTime(),
		)
	}

	Describe("Poll", func() {
		Context("cold start (empty cursor) + repo currently red", func() {
			It("treats cold start as green and publishes on first red", func() {
				ghClient.GetDefaultBranchReturns("main", nil)
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{
						WorkflowID: 1,
						Name:       "CI",
						HeadSHA:    "sha-abc",
						Conclusion: "failure",
						HTMLURL:    "https://github.com/owner/repo/actions/runs/1",
						CreatedAt:  time.Now(),
					},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				Expect(createSender.SendCommandCallCount()).To(Equal(1))
				_, cmd := createSender.SendCommandArgsForCall(0)
				Expect(string(cmd.TaskIdentifier)).NotTo(BeEmpty())
				Expect(cmd.Frontmatter["assignee"]).To(Equal("build-fixer-agent"))
				Expect(cmd.Frontmatter["task_type"]).To(Equal("build-fix"))
				Expect(cmd.Frontmatter["episode_sha"]).To(Equal("sha-abc"))
				Expect(cmd.Title).To(Equal("Build Failure github - owner-repo - sha-abc"))

				// verify cursor updated to red
				loaded, err := pkg.LoadCursor(ctx, cursorPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(loaded.Repos["owner/repo"].LastKnownState).To(Equal("red"))
				Expect(loaded.Repos["owner/repo"].CurrentEpisodeSHA).To(Equal("sha-abc"))
			})
		})

		Context("green → green", func() {
			It("does not publish", func() {
				ghClient.GetDefaultBranchReturns("main", nil)
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-a", Conclusion: "success", CreatedAt: time.Now()},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(0))

				// second poll still green
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(0))
			})
		})

		Context("green → red", func() {
			It("publishes task and updates cursor", func() {
				ghClient.GetDefaultBranchReturns("main", nil)
				// first poll: green
				ghClient.GetWorkflowRunsReturnsOnCall(0, []pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-a", Conclusion: "success", CreatedAt: time.Now()},
				}, nil)
				// second poll: red
				ghClient.GetWorkflowRunsReturnsOnCall(1, []pkg.WorkflowRun{
					{
						WorkflowID: 1,
						Name:       "CI",
						HeadSHA:    "sha-b",
						Conclusion: "failure",
						HTMLURL:    "https://github.com/owner/repo/actions/runs/2",
						CreatedAt:  time.Now(),
					},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(0))

				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(1))
				_, cmd := createSender.SendCommandArgsForCall(0)
				Expect(cmd.Frontmatter["episode_sha"]).To(Equal("sha-b"))
				Expect(cmd.Frontmatter["repo"]).To(Equal("owner/repo"))
				Expect(cmd.Title).To(Equal("Build Failure github - owner-repo - sha-b"))
			})
		})

		Context("red → red (same SHA)", func() {
			It("does not re-publish and keeps episode SHA", func() {
				sha := "sha-abc"
				ghClient.GetDefaultBranchReturns("main", nil)
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{
						WorkflowID: 1,
						HeadSHA:    sha,
						Conclusion: "failure",
						CreatedAt:  time.Now(),
					},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(1))

				// second poll: same SHA still failing
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(1)) // no new publish
			})
		})

		Context("red → red (different SHA from layered commit)", func() {
			It("does not re-publish and keeps the original episode SHA", func() {
				t1 := time.Now()
				t2 := t1.Add(time.Minute)

				ghClient.GetDefaultBranchReturns("main", nil)
				// first poll: red with sha-a
				ghClient.GetWorkflowRunsReturnsOnCall(0, []pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-a", Conclusion: "failure", CreatedAt: t1},
				}, nil)
				// second poll: red with sha-b (new commit, still failing)
				ghClient.GetWorkflowRunsReturnsOnCall(1, []pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-b", Conclusion: "failure", CreatedAt: t2},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(1))

				_, firstCmd := createSender.SendCommandArgsForCall(0)
				Expect(firstCmd.Frontmatter["episode_sha"]).To(Equal("sha-a"))

				Expect(w.Poll(ctx, false)).To(Succeed())
				// no new publish — episode is locked on first red SHA
				Expect(createSender.SendCommandCallCount()).To(Equal(1))

				// cursor still holds original episode SHA
				loaded, err := pkg.LoadCursor(ctx, cursorPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(loaded.Repos["owner/repo"].CurrentEpisodeSHA).To(Equal("sha-a"))
			})
		})

		Context("red → green", func() {
			It("clears episode SHA and does not publish", func() {
				ghClient.GetDefaultBranchReturns("main", nil)
				// first poll: red
				ghClient.GetWorkflowRunsReturnsOnCall(0, []pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-a", Conclusion: "failure", CreatedAt: time.Now()},
				}, nil)
				// second poll: green
				ghClient.GetWorkflowRunsReturnsOnCall(1, []pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-b", Conclusion: "success", CreatedAt: time.Now()},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(1))

				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(1)) // no new publish

				loaded, err := pkg.LoadCursor(ctx, cursorPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(loaded.Repos["owner/repo"].LastKnownState).To(Equal("green"))
				Expect(loaded.Repos["owner/repo"].CurrentEpisodeSHA).To(Equal(""))
			})
		})

		Context("undefined state (zero qualifying runs)", func() {
			It("skips the repo without updating cursor or publishing", func() {
				ghClient.GetDefaultBranchReturns("main", nil)
				// only cancelled runs — all filtered out
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{
						WorkflowID: 1,
						HeadSHA:    "sha-a",
						Conclusion: "cancelled",
						CreatedAt:  time.Now(),
					},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(0))

				loaded, err := pkg.LoadCursor(ctx, cursorPath)
				Expect(err).NotTo(HaveOccurred())
				// repo entry is created but state stays at zero-value ""
				Expect(loaded.Repos["owner/repo"].LastKnownState).To(Equal(""))
			})
		})

		Context("Kafka failure", func() {
			It("does not update cursor so next poll retries", func() {
				ghClient.GetDefaultBranchReturns("main", nil)
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-a", Conclusion: "failure", CreatedAt: time.Now()},
				}, nil)
				createSender.SendCommandReturns(os.ErrProcessDone)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed()) // poll succeeds; kafka error is logged

				Expect(createSender.SendCommandCallCount()).To(Equal(1))

				// cursor must NOT have been updated to red
				loaded, err := pkg.LoadCursor(ctx, cursorPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(loaded.Repos["owner/repo"].LastKnownState).To(Equal(""))
				Expect(loaded.Repos["owner/repo"].CurrentEpisodeSHA).To(Equal(""))
			})
		})

		Context("GitHub API error for one repo", func() {
			It("skips that repo and continues polling remaining repos", func() {
				ghClient.GetDefaultBranchReturns("main", nil)
				// repo-a: API error
				ghClient.GetWorkflowRunsReturnsOnCall(0, nil, os.ErrNotExist)
				// repo-b: success (red)
				ghClient.GetWorkflowRunsReturnsOnCall(1, []pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-b", Conclusion: "failure", CreatedAt: time.Now()},
				}, nil)

				w := makeWatcher([]string{"owner/repo-a", "owner/repo-b"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				// repo-b must still have been processed
				Expect(createSender.SendCommandCallCount()).To(Equal(1))
			})
		})

		Context("rate-limit error", func() {
			It("terminates poll loop early for remaining repos", func() {
				ghClient.GetDefaultBranchReturns("main", nil)
				// repo-a: rate limited
				ghClient.GetWorkflowRunsReturnsOnCall(0, nil, pkg.ErrRateLimited)
				// repo-b: would succeed, but should not be reached
				ghClient.GetWorkflowRunsReturnsOnCall(1, []pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-b", Conclusion: "failure", CreatedAt: time.Now()},
				}, nil)

				w := makeWatcher([]string{"owner/repo-a", "owner/repo-b"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				// repo-b was not reached due to rate-limit break
				Expect(createSender.SendCommandCallCount()).To(Equal(0))
				// rate_limited error metric incremented once
				Expect(metrics.IncPollErrorCallCount()).To(Equal(1))
				Expect(metrics.IncPollErrorArgsForCall(0)).To(Equal("rate_limited"))
			})
		})

		Context("corrupt cursor", func() {
			It("returns error and does not publish", func() {
				Expect(os.WriteFile(cursorPath, []byte("{invalid-json"), 0600)).To(Succeed())

				w := makeWatcher([]string{"owner/repo"})
				err := w.Poll(ctx, false)
				Expect(err).To(HaveOccurred())
				Expect(createSender.SendCommandCallCount()).To(Equal(0))
			})
		})

		Context("worked example: green → red → red(layered) → green → red (new episode)", func() {
			It("correctly tracks episode boundaries and task IDs", func() {
				t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				t1 := t0.Add(time.Hour)
				t2 := t1.Add(time.Hour)
				t3 := t2.Add(time.Hour)
				t4 := t3.Add(time.Hour)

				ghClient.GetDefaultBranchReturns("main", nil)

				// t0: green (success)
				ghClient.GetWorkflowRunsReturnsOnCall(0, []pkg.WorkflowRun{
					{
						WorkflowID: 1,
						Name:       "CI",
						HeadSHA:    "sha-green",
						Conclusion: "success",
						CreatedAt:  t0,
					},
				}, nil)
				// t1: commit A breaks build
				ghClient.GetWorkflowRunsReturnsOnCall(1, []pkg.WorkflowRun{
					{WorkflowID: 1, Name: "CI", HeadSHA: "sha-a", Conclusion: "failure",
						HTMLURL: "https://github.com/owner/repo/actions/runs/1", CreatedAt: t1},
				}, nil)
				// t2: commit B layered, still red with different SHA
				ghClient.GetWorkflowRunsReturnsOnCall(2, []pkg.WorkflowRun{
					{WorkflowID: 1, Name: "CI", HeadSHA: "sha-b", Conclusion: "failure",
						HTMLURL: "https://github.com/owner/repo/actions/runs/2", CreatedAt: t2},
				}, nil)
				// t3: fixed → green
				ghClient.GetWorkflowRunsReturnsOnCall(3, []pkg.WorkflowRun{
					{
						WorkflowID: 1,
						Name:       "CI",
						HeadSHA:    "sha-b",
						Conclusion: "success",
						CreatedAt:  t3,
					},
				}, nil)
				// t4: commit C breaks build — new episode
				ghClient.GetWorkflowRunsReturnsOnCall(4, []pkg.WorkflowRun{
					{WorkflowID: 1, Name: "CI", HeadSHA: "sha-c", Conclusion: "failure",
						HTMLURL: "https://github.com/owner/repo/actions/runs/3", CreatedAt: t4},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})

				// t0: green → no publish
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(0))

				// t1: green → red → publish with episode sha-a
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(1))
				_, cmd1 := createSender.SendCommandArgsForCall(0)
				Expect(cmd1.Frontmatter["episode_sha"]).To(Equal("sha-a"))

				// t2: red → red (sha-b) → no publish, episode SHA stays sha-a
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(1))

				cursor, err := pkg.LoadCursor(ctx, cursorPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(cursor.Repos["owner/repo"].CurrentEpisodeSHA).To(Equal("sha-a"))

				// t3: red → green → no publish
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(1))
				cursor, err = pkg.LoadCursor(ctx, cursorPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(cursor.Repos["owner/repo"].LastKnownState).To(Equal("green"))
				Expect(cursor.Repos["owner/repo"].CurrentEpisodeSHA).To(Equal(""))

				// t4: green → red with sha-c → new publish, new episode
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(createSender.SendCommandCallCount()).To(Equal(2))
				_, cmd4 := createSender.SendCommandArgsForCall(1)
				Expect(cmd4.Frontmatter["episode_sha"]).To(Equal("sha-c"))

				// Task IDs must differ (different episodes)
				Expect(cmd1.TaskIdentifier).NotTo(Equal(cmd4.TaskIdentifier))
			})
		})

		Context("episode SHA: earliest failing run chosen", func() {
			It("picks the HeadSHA of the earliest failing run across workflows", func() {
				early := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
				late := early.Add(time.Hour)

				ghClient.GetDefaultBranchReturns("main", nil)
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{
						WorkflowID: 1,
						Name:       "CI",
						HeadSHA:    "sha-late",
						Conclusion: "failure",
						CreatedAt:  late,
					},
					{
						WorkflowID: 2,
						Name:       "Deploy",
						HeadSHA:    "sha-early",
						Conclusion: "failure",
						CreatedAt:  early,
					},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				Expect(createSender.SendCommandCallCount()).To(Equal(1))
				_, cmd := createSender.SendCommandArgsForCall(0)
				// earliest failing run is sha-early (smaller CreatedAt)
				Expect(cmd.Frontmatter["episode_sha"]).To(Equal("sha-early"))
			})
		})

		Context("deduplication: latest run per workflow kept", func() {
			It("considers only the latest run per WorkflowID", func() {
				t1 := time.Now()
				t2 := t1.Add(time.Minute)

				ghClient.GetDefaultBranchReturns("main", nil)
				// Workflow 1 had a failure, then a success — latest is success
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-old", Conclusion: "failure", CreatedAt: t1},
					{WorkflowID: 1, HeadSHA: "sha-new", Conclusion: "success", CreatedAt: t2},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				// latest run is success → state is green → no publish
				Expect(createSender.SendCommandCallCount()).To(Equal(0))
			})
		})

		Context("assignee contract", func() {
			It("sets assignee to build-fixer-agent in the task command", func() {
				ghClient.GetDefaultBranchReturns("main", nil)
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{WorkflowID: 1, HeadSHA: "sha-a", Conclusion: "failure", CreatedAt: time.Now()},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				Expect(createSender.SendCommandCallCount()).To(Equal(1))
				_, cmd := createSender.SendCommandArgsForCall(0)
				Expect(cmd.Frontmatter["assignee"]).To(Equal("build-fixer-agent"))
				Expect(cmd.Frontmatter["task_type"]).To(Equal("build-fix"))
			})
		})

		Context("host-prefixed allowlist entry (github.com/owner/repo)", func() {
			It(
				"strips host for GitHub API calls, keeps host in cursor key, strips host in task body",
				func() {
					ghClient.GetDefaultBranchReturns("main", nil)
					ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
						{
							WorkflowID: 1,
							Name:       "CI",
							HeadSHA:    "sha-abc",
							Conclusion: "failure",
							HTMLURL:    "https://github.com/owner/repo/actions/runs/1",
							CreatedAt:  time.Now(),
						},
					}, nil)

					w := makeWatcher([]string{"github.com/owner/repo"})
					Expect(w.Poll(ctx, false)).To(Succeed())

					// GitHub API must receive the stripped owner and repo — not the host
					Expect(ghClient.GetDefaultBranchCallCount()).To(Equal(1))
					_, gotOwner, gotRepo := ghClient.GetDefaultBranchArgsForCall(0)
					Expect(gotOwner).To(Equal("owner"))
					Expect(gotRepo).To(Equal("repo"))

					// task published with stripped form in frontmatter and body
					Expect(createSender.SendCommandCallCount()).To(Equal(1))
					_, cmd := createSender.SendCommandArgsForCall(0)
					Expect(cmd.Frontmatter["repo"]).To(Equal("owner/repo"))
					Expect(
						cmd.Body,
					).To(ContainSubstring("# Build Failure: [owner/repo](https://github.com/owner/repo)"))
					Expect(
						cmd.Title,
					).To(Equal("Build Failure github - owner-repo - sha-abc"))

					// cursor key keeps the host prefix
					loaded, err := pkg.LoadCursor(ctx, cursorPath)
					Expect(err).NotTo(HaveOccurred())
					Expect(loaded.Repos).To(HaveKey("github.com/owner/repo"))
					Expect(loaded.Repos).NotTo(HaveKey("owner/repo"))
					Expect(loaded.Repos["github.com/owner/repo"].LastKnownState).To(Equal("red"))
				},
			)
		})
	})

	Describe("configurable frontmatter", func() {
		makeCustomWatcher := func(allowlist []string, assignee, taskStatus, taskPhase string) pkg.Watcher {
			ml := new(mocks.MaintenanceLoader)
			ml.LoadOverridesReturns(maintenance.GithubBuildConfig{})
			return pkg.NewWatcher(
				ghClient,
				createSender,
				metrics,
				filter.RepoFilters{},
				pkg.NewStaticSnapshot(allowlist),
				cursorPath,
				assignee,
				taskStatus,
				taskPhase,
				ml,
				pkg.DefaultMaxTitleLen,
				"",
				libtime.NewCurrentDateTime(),
			)
		}

		singleFailingRun := func(workflowID int64, sha string) []pkg.WorkflowRun {
			return []pkg.WorkflowRun{
				{
					WorkflowID: workflowID,
					Name:       "CI",
					HeadSHA:    sha,
					Conclusion: "failure",
					HTMLURL:    "https://github.com/owner/repo/actions/runs/99",
					CreatedAt:  time.Now(),
				},
			}
		}

		It("uses custom assignee and status when set", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRun(10, "sha-custom"), nil)

			w := makeCustomWatcher([]string{"owner/repo"}, "other-agent", "backlog", "")
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter["assignee"]).To(Equal("other-agent"))
			Expect(cmd.Frontmatter["task_type"]).To(Equal("build-fix"))
			Expect(cmd.Frontmatter["status"]).To(Equal("backlog"))
			Expect(cmd.Frontmatter).NotTo(HaveKey("phase"))
		})

		It("translates assignee=human to empty string in emitted frontmatter", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRun(20, "sha-human"), nil)

			w := makeCustomWatcher([]string{"owner/repo"}, "human", "todo", "")
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter["task_type"]).To(Equal("build-fix"))
			Expect(cmd.Frontmatter["assignee"]).To(Equal(""))
		})

		It("passes through assignee=build-fix-planner unchanged", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRun(21, "sha-planner"), nil)

			w := makeCustomWatcher([]string{"owner/repo"}, "build-fix-planner", "todo", "")
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter["task_type"]).To(Equal("build-fix"))
			Expect(cmd.Frontmatter["assignee"]).To(Equal("build-fix-planner"))
		})

		It("includes phase key when WATCHER_GITHUB_BUILD_TASK_PHASE is non-empty", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRun(11, "sha-phase"), nil)

			w := makeCustomWatcher([]string{"owner/repo"}, "build-fixer-agent", "todo", "planning")
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter["phase"]).To(Equal("planning"))
		})

		It("omits phase key when WATCHER_GITHUB_BUILD_TASK_PHASE is empty string", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRun(12, "sha-nophase"), nil)

			w := makeCustomWatcher([]string{"owner/repo"}, "build-fixer-agent", "todo", "")
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter).NotTo(HaveKey("phase"))
		})
	})

	Describe("per-repo maintenance overrides", func() {
		var maintenanceLoader *mocks.MaintenanceLoader

		makeWatcherWithLoader := func(allowlist []string, loader maintenance.Loader) pkg.Watcher {
			return pkg.NewWatcher(
				ghClient,
				createSender,
				metrics,
				filter.RepoFilters{},
				pkg.NewStaticSnapshot(allowlist),
				cursorPath,
				"build-fixer-agent",
				"todo",
				"",
				loader,
				pkg.DefaultMaxTitleLen,
				"",
				libtime.NewCurrentDateTime(),
			)
		}

		singleFailingRunMaint := func(sha string) []pkg.WorkflowRun {
			return []pkg.WorkflowRun{
				{
					WorkflowID: 999,
					Name:       "CI",
					HeadSHA:    sha,
					Conclusion: "failure",
					HTMLURL:    "https://github.com/owner/repo/actions/runs/1",
					CreatedAt:  time.Now(),
				},
			}
		}

		singleSuccessRunMaint := func(sha string) []pkg.WorkflowRun {
			return []pkg.WorkflowRun{
				{
					WorkflowID: 999,
					Name:       "CI",
					HeadSHA:    sha,
					Conclusion: "success",
					CreatedAt:  time.Now(),
				},
			}
		}

		BeforeEach(func() {
			maintenanceLoader = new(mocks.MaintenanceLoader)
			maintenanceLoader.LoadOverridesReturns(maintenance.GithubBuildConfig{})
		})

		It("uses watcher defaults when maintenance file returns empty config", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRunMaint("sha-default"), nil)
			maintenanceLoader.LoadOverridesReturns(maintenance.GithubBuildConfig{})

			w := makeWatcherWithLoader([]string{"owner/repo"}, maintenanceLoader)
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter["assignee"]).To(Equal("build-fixer-agent"))
			Expect(cmd.Frontmatter["task_type"]).To(Equal("build-fix"))
			Expect(cmd.Frontmatter["status"]).To(Equal("todo"))
			Expect(cmd.Frontmatter).NotTo(HaveKey("phase"))
		})

		It("overrides all three fields when the maintenance file provides them", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRunMaint("sha-override"), nil)
			maintenanceLoader.LoadOverridesReturns(maintenance.GithubBuildConfig{
				Assignee: "go-deps-fixer-agent",
				Status:   "backlog",
				Phase:    "planning",
			})

			w := makeWatcherWithLoader([]string{"owner/repo"}, maintenanceLoader)
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter["assignee"]).To(Equal("go-deps-fixer-agent"))
			Expect(cmd.Frontmatter["task_type"]).To(Equal("build-fix"))
			Expect(cmd.Frontmatter["status"]).To(Equal("backlog"))
			Expect(cmd.Frontmatter["phase"]).To(Equal("planning"))
		})

		It("overrides only assignee; watcher defaults apply for status and phase", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRunMaint("sha-partial"), nil)
			maintenanceLoader.LoadOverridesReturns(maintenance.GithubBuildConfig{
				Assignee: "other-agent",
			})

			w := makeWatcherWithLoader([]string{"owner/repo"}, maintenanceLoader)
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter["assignee"]).To(Equal("other-agent"))
			Expect(cmd.Frontmatter["status"]).To(Equal("todo"))
			Expect(cmd.Frontmatter).NotTo(HaveKey("phase"))
		})

		It("empty assignee in file falls through to watcher default", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRunMaint("sha-empty"), nil)
			maintenanceLoader.LoadOverridesReturns(maintenance.GithubBuildConfig{
				Assignee: "",
			})

			w := makeWatcherWithLoader([]string{"owner/repo"}, maintenanceLoader)
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter["assignee"]).To(Equal("build-fixer-agent"))
		})

		It("translates maintenance override assignee=human to empty string", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRunMaint("sha-human-override"), nil)
			maintenanceLoader.LoadOverridesReturns(maintenance.GithubBuildConfig{
				Assignee: "human",
			})

			w := makeWatcherWithLoader([]string{"owner/repo"}, maintenanceLoader)
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Frontmatter["task_type"]).To(Equal("build-fix"))
			Expect(cmd.Frontmatter["assignee"]).To(Equal(""))
		})

		It("loader is NOT called on red→red (no wasted API call)", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleFailingRunMaint("sha-red"), nil)
			w := makeWatcherWithLoader([]string{"owner/repo"}, maintenanceLoader)
			Expect(w.Poll(ctx, false)).To(Succeed())
			callsAfterFirst := maintenanceLoader.LoadOverridesCallCount()
			Expect(callsAfterFirst).To(Equal(1))

			Expect(w.Poll(ctx, false)).To(Succeed())
			Expect(maintenanceLoader.LoadOverridesCallCount()).To(Equal(callsAfterFirst))
		})

		It("loader is NOT called on green→green (steady state, no publish)", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns(singleSuccessRunMaint("sha-green"), nil)
			w := makeWatcherWithLoader([]string{"owner/repo"}, maintenanceLoader)
			Expect(w.Poll(ctx, false)).To(Succeed())
			Expect(w.Poll(ctx, false)).To(Succeed())
			Expect(maintenanceLoader.LoadOverridesCallCount()).To(Equal(0))
			Expect(createSender.SendCommandCallCount()).To(Equal(0))
		})

		It("loader is NOT called on red→green (clear-state path; no publish)", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturnsOnCall(0, singleFailingRunMaint("sha-red"), nil)
			w := makeWatcherWithLoader([]string{"owner/repo"}, maintenanceLoader)
			Expect(w.Poll(ctx, false)).To(Succeed())
			callsAfterRed := maintenanceLoader.LoadOverridesCallCount()
			Expect(callsAfterRed).To(Equal(1))

			ghClient.GetWorkflowRunsReturnsOnCall(1, singleSuccessRunMaint("sha-green"), nil)
			Expect(w.Poll(ctx, false)).To(Succeed())
			Expect(maintenanceLoader.LoadOverridesCallCount()).To(Equal(callsAfterRed))
			Expect(createSender.SendCommandCallCount()).To(Equal(1))
		})
	})

	Describe("task body header context", func() {
		var t0 time.Time

		BeforeEach(func() {
			t0 = time.Date(2026, 5, 6, 14, 32, 0, 0, time.UTC)
		})

		It("includes all header fields when WorkflowRun has full context", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
				{
					WorkflowID:   1,
					RunID:        42,
					Name:         "CI",
					HeadSHA:      "sha-abc",
					Conclusion:   "failure",
					HTMLURL:      "https://github.com/owner/repo/actions/runs/42",
					CreatedAt:    t0,
					DisplayTitle: "Fix authentication bug",
					HeadBranch:   "main",
					Event:        "push",
					StartedAt:    t0,
					UpdatedAt:    t0.Add(3*time.Minute + 47*time.Second),
				},
			}, nil)

			w := makeWatcher([]string{"owner/repo"})
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).To(ContainSubstring("**Commit:** Fix authentication bug"))
			Expect(cmd.Body).To(ContainSubstring("**Branch:** main"))
			Expect(cmd.Body).To(ContainSubstring("**Event:** push"))
			Expect(cmd.Body).To(ContainSubstring("**Started:** 2026-05-06T14:32:00Z"))
			Expect(cmd.Body).To(ContainSubstring("**Finished:** 2026-05-06T14:35:47Z"))
			Expect(cmd.Body).To(ContainSubstring("**Duration:** 3m 47s"))
		})

		It("omits all header fields when WorkflowRun has zero context (backwards compat)", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
				{
					WorkflowID: 1,
					HeadSHA:    "sha-abc",
					Conclusion: "failure",
					CreatedAt:  time.Now(),
					// DisplayTitle, HeadBranch, Event, StartedAt, UpdatedAt all zero
				},
			}, nil)

			w := makeWatcher([]string{"owner/repo"})
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).NotTo(ContainSubstring("**Commit:**"))
			Expect(cmd.Body).NotTo(ContainSubstring("**Branch:**"))
			Expect(cmd.Body).NotTo(ContainSubstring("**Duration:**"))
			Expect(cmd.Body).To(ContainSubstring("Episode SHA: `sha-abc`"))
			Expect(cmd.Body).To(ContainSubstring("## Failing Workflows"))
		})

		It("omits Duration when only StartedAt is set (UpdatedAt zero)", func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
				{
					WorkflowID: 1,
					HeadSHA:    "sha-abc",
					Conclusion: "failure",
					CreatedAt:  time.Now(),
					StartedAt:  t0,
					// UpdatedAt zero
				},
			}, nil)

			w := makeWatcher([]string{"owner/repo"})
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).To(ContainSubstring("**Started:** 2026-05-06T14:32:00Z"))
			Expect(cmd.Body).NotTo(ContainSubstring("**Finished:**"))
			Expect(cmd.Body).NotTo(ContainSubstring("**Duration:**"))
		})

		It("uses earliest failing run for header context when multiple runs fail", func() {
			early := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
			late := early.Add(time.Hour)

			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
				{
					WorkflowID:   1,
					RunID:        10,
					Name:         "CI",
					HeadSHA:      "sha-late",
					Conclusion:   "failure",
					HTMLURL:      "https://github.com/owner/repo/actions/runs/10",
					CreatedAt:    late,
					DisplayTitle: "Late commit",
					HeadBranch:   "feature",
				},
				{
					WorkflowID:   2,
					RunID:        20,
					Name:         "Deploy",
					HeadSHA:      "sha-early",
					Conclusion:   "failure",
					HTMLURL:      "https://github.com/owner/repo/actions/runs/20",
					CreatedAt:    early,
					DisplayTitle: "Early commit",
					HeadBranch:   "main",
				},
			}, nil)

			w := makeWatcher([]string{"owner/repo"})
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).To(ContainSubstring("**Commit:** Early commit"))
			Expect(cmd.Body).To(ContainSubstring("**Branch:** main"))
			Expect(cmd.Frontmatter["episode_sha"]).To(Equal("sha-early"))
		})
	})

	Describe("include_logs opt-in", func() {
		var maintenanceLoaderWithLogs *mocks.MaintenanceLoader
		var runID int64 = 77
		var jobID int64 = 99

		makeWatcherWithLogs := func(includeLogs bool) pkg.Watcher {
			maintenanceLoaderWithLogs = new(mocks.MaintenanceLoader)
			maintenanceLoaderWithLogs.LoadOverridesReturns(maintenance.GithubBuildConfig{
				IncludeLogs: includeLogs,
			})
			return pkg.NewWatcher(
				ghClient,
				createSender,
				metrics,
				filter.RepoFilters{},
				pkg.NewStaticSnapshot([]string{"owner/repo"}),
				cursorPath,
				"build-fixer-agent",
				"todo",
				"",
				maintenanceLoaderWithLogs,
				pkg.DefaultMaxTitleLen,
				"",
				libtime.NewCurrentDateTime(),
			)
		}

		singleFailingRunWithJobID := func(sha string) []pkg.WorkflowRun {
			return []pkg.WorkflowRun{
				{
					WorkflowID: 1,
					RunID:      runID,
					Name:       "CI",
					HeadSHA:    sha,
					Conclusion: "failure",
					HTMLURL:    "https://github.com/owner/repo/actions/runs/77",
					CreatedAt:  time.Now(),
				},
			}
		}

		BeforeEach(func() {
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetJobsForRunReturns([]pkg.WorkflowJobInfo{
				{JobID: jobID, JobName: "build", FailedStepName: "Run tests"},
			}, nil)
		})

		It("emits ## Error section with redacted snippet when include_logs=true", func() {
			ghClient.GetWorkflowRunsReturns(singleFailingRunWithJobID("sha-logs"), nil)
			// Log contains a GitHub token that should be redacted
			logContent := "step 1: ok\nstep 2: token=ghp_ABCDEFGHIJKLMNOPqrstu\nstep 3: FAILED"
			ghClient.GetJobLogReturns([]byte(logContent), nil)

			w := makeWatcherWithLogs(true)
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).To(ContainSubstring("## Error"))
			Expect(cmd.Body).To(ContainSubstring("```"))
			// Token must be redacted
			Expect(cmd.Body).NotTo(ContainSubstring("ghp_ABCDEFGHIJKLMNOPqrstu"))
			Expect(cmd.Body).To(ContainSubstring("[REDACTED]"))
			// Log content (sans token) must be present
			Expect(cmd.Body).To(ContainSubstring("step 3: FAILED"))
		})

		It("omits ## Error section when include_logs=false (default)", func() {
			ghClient.GetWorkflowRunsReturns(singleFailingRunWithJobID("sha-nologs"), nil)

			w := makeWatcherWithLogs(false)
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).NotTo(ContainSubstring("## Error"))
			// GetJobLog must NOT be called when include_logs=false
			Expect(ghClient.GetJobLogCallCount()).To(Equal(0))
		})

		It("omits ## Error section when log fetch fails; publish still succeeds", func() {
			ghClient.GetWorkflowRunsReturns(singleFailingRunWithJobID("sha-logfail"), nil)
			ghClient.GetJobLogReturns(nil, os.ErrNotExist)

			w := makeWatcherWithLogs(true)
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			// Body still has the table — just no ## Error section
			Expect(cmd.Body).To(ContainSubstring("## Failing Workflows"))
			Expect(cmd.Body).NotTo(ContainSubstring("## Error"))
		})

		It("omits ## Error when jobs API fails (no jobID to log-fetch with)", func() {
			ghClient.GetWorkflowRunsReturns(singleFailingRunWithJobID("sha-nojob"), nil)
			// jobs API failure → primaryJobID stays 0 → log fetch skipped
			ghClient.GetJobsForRunReturns(nil, os.ErrNotExist)
			ghClient.GetJobLogReturns(nil, nil)

			w := makeWatcherWithLogs(true)
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).NotTo(ContainSubstring("## Error"))
			Expect(ghClient.GetJobLogCallCount()).To(Equal(0))
		})
	})

	Describe("failing workflows table", func() {
		var runID int64 = 42

		singleFailingRunWithID := func(sha string) []pkg.WorkflowRun {
			return []pkg.WorkflowRun{
				{
					WorkflowID: 1,
					RunID:      runID,
					Name:       "CI",
					HeadSHA:    sha,
					Conclusion: "failure",
					HTMLURL:    "https://github.com/owner/repo/actions/runs/42",
					CreatedAt:  time.Now(),
				},
			}
		}

		BeforeEach(func() {
			ghClient.GetDefaultBranchReturns("main", nil)
		})

		It("emits a table with job name and step name when jobs API succeeds", func() {
			ghClient.GetWorkflowRunsReturns(singleFailingRunWithID("sha-jobs"), nil)
			ghClient.GetJobsForRunReturns([]pkg.WorkflowJobInfo{
				{JobID: 99, JobName: "build", FailedStepName: "Run tests"},
			}, nil)

			w := makeWatcher([]string{"owner/repo"})
			Expect(w.Poll(ctx, false)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).To(ContainSubstring("| Workflow | Job | Failed Step | Run |"))
			Expect(
				cmd.Body,
			).To(ContainSubstring("| CI | build | Run tests | [Run](https://github.com/owner/repo/actions/runs/42) |"))
		})

		It("shows ? for job and step when jobs API returns an error", func() {
			ghClient.GetWorkflowRunsReturns(singleFailingRunWithID("sha-err"), nil)
			ghClient.GetJobsForRunReturns(nil, stderrors.New("http 503 service unavailable"))

			w := makeWatcher([]string{"owner/repo"})
			Expect(w.Poll(ctx, false)).To(Succeed())

			// Publish still succeeds despite jobs API failure
			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).To(ContainSubstring("| CI | ? | ? |"))
		})

		It("shows ? for step when jobs API returns a job with no failed step", func() {
			ghClient.GetWorkflowRunsReturns(singleFailingRunWithID("sha-nostep"), nil)
			ghClient.GetJobsForRunReturns([]pkg.WorkflowJobInfo{
				{JobID: 99, JobName: "build", FailedStepName: ""},
			}, nil)

			w := makeWatcher([]string{"owner/repo"})
			Expect(w.Poll(ctx, false)).To(Succeed())

			_, cmd := createSender.SendCommandArgsForCall(0)
			Expect(cmd.Body).To(ContainSubstring("| CI | build | ? |"))
		})

		It("calls GetJobsForRun exactly once per failing run", func() {
			early := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
			late := early.Add(time.Minute)

			ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
				{WorkflowID: 1, RunID: 10, Name: "CI", HeadSHA: "sha-a",
					Conclusion: "failure", HTMLURL: "https://x/1", CreatedAt: early},
				{WorkflowID: 2, RunID: 20, Name: "Lint", HeadSHA: "sha-a",
					Conclusion: "failure", HTMLURL: "https://x/2", CreatedAt: late},
			}, nil)
			ghClient.GetJobsForRunReturns([]pkg.WorkflowJobInfo{
				{JobID: 1, JobName: "check", FailedStepName: "golangci-lint"},
			}, nil)

			w := makeWatcher([]string{"owner/repo"})
			Expect(w.Poll(ctx, false)).To(Succeed())

			// Exactly 2 calls — one per failing run (not one per job or step)
			Expect(ghClient.GetJobsForRunCallCount()).To(Equal(2))
			_, _, _, runID1 := ghClient.GetJobsForRunArgsForCall(0)
			_, _, _, runID2 := ghClient.GetJobsForRunArgsForCall(1)
			Expect([]int64{runID1, runID2}).To(ConsistOf(int64(10), int64(20)))
		})

		It(
			"table still renders on second poll (red→red) without additional GetJobsForRun calls",
			func() {
				ghClient.GetWorkflowRunsReturns(singleFailingRunWithID("sha-locked"), nil)
				ghClient.GetJobsForRunReturns([]pkg.WorkflowJobInfo{
					{JobID: 99, JobName: "build", FailedStepName: "Run tests"},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())
				firstCallCount := ghClient.GetJobsForRunCallCount()
				Expect(firstCallCount).To(Equal(1))

				// Second poll: red→red — no publish, no GetJobsForRun call
				Expect(w.Poll(ctx, false)).To(Succeed())
				Expect(ghClient.GetJobsForRunCallCount()).To(Equal(firstCallCount))
				Expect(createSender.SendCommandCallCount()).To(Equal(1)) // still only 1 publish
			},
		)
	})

	Describe("Dependabot graph-update workflow filter", func() {
		var t0 time.Time

		BeforeEach(func() {
			t0 = time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
			ghClient.GetDefaultBranchReturns("main", nil)
		})

		Context("pure Dependabot case: Graph Update: go_modules", func() {
			It("emits zero CreateTaskCommands (workflow filtered)", func() {
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{
						WorkflowID: 1,
						Name:       "Graph Update: go_modules",
						HeadSHA:    "sha-dep",
						Conclusion: "failure",
						HTMLURL:    "https://github.com/owner/repo/actions/runs/99",
						CreatedAt:  t0,
					},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				// Zero tasks — the Dependabot workflow is filtered out
				Expect(createSender.SendCommandCallCount()).To(Equal(0))
			})
		})

		Context("pure Dependabot case: Dependabot Updates", func() {
			It("emits zero CreateTaskCommands (workflow filtered)", func() {
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{
						WorkflowID: 2,
						Name:       "Dependabot Updates",
						HeadSHA:    "sha-dep2",
						Conclusion: "failure",
						HTMLURL:    "https://github.com/owner/repo/actions/runs/88",
						CreatedAt:  t0,
					},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				Expect(createSender.SendCommandCallCount()).To(Equal(0))
			})
		})

		Context("mixed case: real CI fails alongside Graph Update: go_modules", func() {
			It(
				"emits exactly one CreateTaskCommand and the body references the CI workflow, not the Dependabot one",
				func() {
					ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
						{
							WorkflowID: 1,
							Name:       "Graph Update: go_modules",
							HeadSHA:    "sha-ci",
							Conclusion: "failure",
							HTMLURL:    "https://github.com/owner/repo/actions/runs/99",
							CreatedAt:  t0,
						},
						{
							WorkflowID: 2,
							Name:       "CI",
							HeadSHA:    "sha-ci",
							Conclusion: "failure",
							HTMLURL:    "https://github.com/owner/repo/actions/runs/1",
							CreatedAt:  t0.Add(time.Second),
						},
					}, nil)

					w := makeWatcher([]string{"owner/repo"})
					Expect(w.Poll(ctx, false)).To(Succeed())

					Expect(createSender.SendCommandCallCount()).To(Equal(1))
					_, cmd := createSender.SendCommandArgsForCall(0)
					Expect(cmd.Frontmatter["repo"]).To(Equal("owner/repo"))

					// CRITICAL: the emitted task must derive from the CI workflow, NOT the Dependabot one.
					// The task body carries the workflow name in the table; verify it does not contain "Graph Update".
					taskBody := cmd.Body
					Expect(taskBody).NotTo(ContainSubstring("Graph Update"))
					Expect(taskBody).To(ContainSubstring("CI"))
				},
			)
		})

		Context("case sensitivity guard: lowercase graph update: x", func() {
			It("emits one CreateTaskCommand (lowercase does not match filter)", func() {
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{
						WorkflowID: 1,
						Name:       "graph update: x",
						HeadSHA:    "sha-lower",
						Conclusion: "failure",
						HTMLURL:    "https://github.com/owner/repo/actions/runs/1",
						CreatedAt:  t0,
					},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				Expect(createSender.SendCommandCallCount()).To(Equal(1))
			})
		})

		Context("nil/empty name guard: empty string workflow name", func() {
			It("emits one CreateTaskCommand (empty name is non-matching, does not crash)", func() {
				ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
					{
						WorkflowID: 1,
						Name:       "",
						HeadSHA:    "sha-empty",
						Conclusion: "failure",
						HTMLURL:    "https://github.com/owner/repo/actions/runs/1",
						CreatedAt:  t0,
					},
				}, nil)

				w := makeWatcher([]string{"owner/repo"})
				Expect(w.Poll(ctx, false)).To(Succeed())

				Expect(createSender.SendCommandCallCount()).To(Equal(1))
			})
		})
	})

	// Force arm (spec 069): with prevState=="red" && currState=="red", the
	// state machine normally skips (episode lock). When force=true the
	// arm publishes a CreateTaskCommand whose TaskIdentifier is salted
	// via DeriveTaskIDForce so the controller's deterministic-ID dedup
	// does not fire and a fresh vault task is created.
	Describe("force arm (spec 069)", func() {
		const (
			owner      = "owner"
			repo       = "repo"
			repoKey    = "owner/repo"
			episodeSHA = "abc123def456"
		)

		var fakeNow libtime.DateTime
		var clock libtime.CurrentDateTimeGetter

		// makeForceWatcher constructs a watcher with the locked-red cursor
		// pre-populated and an injectable clock so the microsecond nonce
		// is deterministic per test.
		makeForceWatcher := func() pkg.Watcher {
			// Pre-populate cursor: repo is red with episodeSHA.
			cursor := &pkg.Cursor{
				Repos: map[string]*pkg.RepoState{
					repoKey: {
						LastKnownState:    "red",
						CurrentEpisodeSHA: episodeSHA,
						DefaultBranch:     "main",
					},
				},
			}
			Expect(pkg.SaveCursor(ctx, cursorPath, cursor)).To(Succeed())

			// GitHub still reports red on the same SHA (red×red case).
			ghClient.GetDefaultBranchReturns("main", nil)
			ghClient.GetWorkflowRunsReturns([]pkg.WorkflowRun{
				{
					WorkflowID: 1,
					Name:       "CI",
					HeadSHA:    episodeSHA,
					Conclusion: "failure",
					HTMLURL:    "https://github.com/owner/repo/actions/runs/1",
					CreatedAt:  time.Now(),
				},
			}, nil)

			ml := new(mocks.MaintenanceLoader)
			ml.LoadOverridesReturns(maintenance.GithubBuildConfig{})
			return pkg.NewWatcher(
				ghClient,
				createSender,
				metrics,
				filter.RepoFilters{},
				pkg.NewStaticSnapshot([]string{repoKey}),
				cursorPath,
				"build-fixer-agent",
				"todo",
				"",
				ml,
				pkg.DefaultMaxTitleLen,
				"",
				clock,
			)
		}

		BeforeEach(func() {
			fakeNow = libtime.DateTime(time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC))
			clock = libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime { return fakeNow })
		})

		It("Poll(ctx, false) does NOT publish on red×red (episode lock holds)", func() {
			w := makeForceWatcher()
			Expect(w.Poll(ctx, false)).To(Succeed())
			Expect(createSender.SendCommandCallCount()).To(Equal(0))
		})

		It("Poll(ctx, true) publishes exactly once with salted TaskIdentifier", func() {
			w := makeForceWatcher()
			Expect(w.Poll(ctx, true)).To(Succeed())

			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, cmd := createSender.SendCommandArgsForCall(0)

			canonical := pkg.DeriveTaskID(owner, repo, episodeSHA).String()
			Expect(string(cmd.TaskIdentifier)).NotTo(Equal(canonical),
				"force arm must use DeriveTaskIDForce, not the canonical DeriveTaskID")
		})

		It("two Poll(ctx, true) with clock advance produce distinct TaskIdentifiers", func() {
			w := makeForceWatcher()

			Expect(w.Poll(ctx, true)).To(Succeed())
			Expect(createSender.SendCommandCallCount()).To(Equal(1))
			_, first := createSender.SendCommandArgsForCall(0)

			// Advance clock by 1µs so UnixMicro() returns a different nonce.
			fakeNow = libtime.DateTime(time.Time(fakeNow).Add(time.Microsecond))

			Expect(w.Poll(ctx, true)).To(Succeed())
			Expect(createSender.SendCommandCallCount()).To(Equal(2))
			_, second := createSender.SendCommandArgsForCall(1)

			Expect(string(first.TaskIdentifier)).NotTo(Equal(string(second.TaskIdentifier)),
				"two forced publishes with distinct nonces must yield distinct identifiers")
		})
	})
})
