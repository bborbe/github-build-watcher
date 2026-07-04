// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	"github.com/bborbe/github-build-watcher/pkg"
	gogithub "github.com/google/go-github/v62/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pkg.GitHubClient", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
	})

	AfterEach(func() {
		cancel()
	})

	buildClient := func(server *httptest.Server) pkg.GitHubClient {
		ghc := gogithub.NewClient(server.Client())
		baseURL, _ := url.Parse(server.URL + "/")
		ghc.BaseURL = baseURL
		return pkg.NewForTest(ghc)
	}

	Describe("GetWorkflowRuns", func() {
		Context("server returns two completed workflow runs", func() {
			It("returns both runs with correct field mapping", func() {
				t1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
				t2 := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{
							"total_count": 2,
							"workflow_runs": [
								{
									"id": 1,
									"workflow_id": 101,
									"name": "CI",
									"head_sha": "abc123",
									"conclusion": "failure",
									"html_url": "https://github.com/owner/repo/actions/runs/1",
									"created_at": "%s"
								},
								{
									"id": 2,
									"workflow_id": 102,
									"name": "Build",
									"head_sha": "def456",
									"conclusion": "success",
									"html_url": "https://github.com/owner/repo/actions/runs/2",
									"created_at": "%s"
								}
							]
						}`, t1.Format(time.RFC3339), t2.Format(time.RFC3339))
					}),
				)
				defer server.Close()

				client := buildClient(server)
				result, err := client.GetWorkflowRuns(ctx, "owner", "repo", "main")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))

				Expect(result[0].WorkflowID).To(Equal(int64(101)))
				Expect(result[0].Name).To(Equal("CI"))
				Expect(result[0].HeadSHA).To(Equal("abc123"))
				Expect(result[0].Conclusion).To(Equal("failure"))
				Expect(result[0].HTMLURL).To(Equal("https://github.com/owner/repo/actions/runs/1"))
				Expect(result[0].CreatedAt).To(BeTemporally("~", t1, time.Second))

				Expect(result[1].WorkflowID).To(Equal(int64(102)))
				Expect(result[1].Conclusion).To(Equal("success"))
			})
		})

		Context("server returns a mix of completed and in-progress runs", func() {
			It("filters out runs with empty Conclusion", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{
							"total_count": 3,
							"workflow_runs": [
								{
									"id": 1,
									"workflow_id": 201,
									"name": "CI",
									"head_sha": "sha1",
									"conclusion": "failure",
									"html_url": "https://github.com/owner/repo/actions/runs/1",
									"created_at": "2026-01-01T12:00:00Z"
								},
								{
									"id": 2,
									"workflow_id": 202,
									"name": "Deploy",
									"head_sha": "sha2",
									"conclusion": "",
									"html_url": "https://github.com/owner/repo/actions/runs/2",
									"created_at": "2026-01-01T12:00:00Z"
								},
								{
									"id": 3,
									"workflow_id": 203,
									"name": "Lint",
									"head_sha": "sha3",
									"conclusion": "success",
									"html_url": "https://github.com/owner/repo/actions/runs/3",
									"created_at": "2026-01-01T12:00:00Z"
								}
							]
						}`)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				result, err := client.GetWorkflowRuns(ctx, "owner", "repo", "main")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))
				for _, run := range result {
					Expect(run.Conclusion).NotTo(BeEmpty())
				}
			})
		})

		Context("API returns HTTP error", func() {
			It("returns a non-nil error", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusUnauthorized)
						fmt.Fprintf(w, `{"message":"Bad credentials"}`)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				_, err := client.GetWorkflowRuns(ctx, "owner", "repo", "main")
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("GetDefaultBranch", func() {
		It("returns the default_branch field from the repository", func() {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/repos/owner/repo"))
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id": 1, "name": "repo", "default_branch": "master"}`)
				}),
			)
			defer server.Close()

			client := buildClient(server)
			result, err := client.GetDefaultBranch(ctx, "owner", "repo")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("master"))
		})

		It("returns an error on HTTP failure", func() {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprintf(w, `{"message":"Not Found"}`)
				}),
			)
			defer server.Close()

			client := buildClient(server)
			_, err := client.GetDefaultBranch(ctx, "owner", "repo")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("GetFileContent", func() {
		Context("file exists", func() {
			It("returns the decoded file content", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.URL.Path).To(Equal("/repos/owner/repo/contents/.maintenance.yaml"))
						w.Header().Set("Content-Type", "application/json")
						// "hello: world\n" base64-encoded
						fmt.Fprintf(
							w,
							`{"type":"file","encoding":"base64","content":"aGVsbG86IHdvcmxkCg==\n","size":14}`,
						)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				content, err := client.GetFileContent(
					ctx,
					"owner",
					"repo",
					".maintenance.yaml",
					"main",
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(content).NotTo(BeNil())
				Expect(string(content)).To(Equal("hello: world\n"))
			})
		})

		Context("file not found (404)", func() {
			It("returns (nil, nil) silently", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprintf(w, `{"message":"Not Found"}`)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				content, err := client.GetFileContent(
					ctx,
					"owner",
					"repo",
					".maintenance.yaml",
					"main",
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(BeNil())
			})
		})

		Context("server returns HTTP 500", func() {
			It("returns a non-nil error", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusInternalServerError)
						fmt.Fprintf(w, `{"message":"Internal Server Error"}`)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				_, err := client.GetFileContent(ctx, "owner", "repo", ".maintenance.yaml", "main")
				Expect(err).To(HaveOccurred())
			})
		})

		Context("path resolves to a directory (API returns array)", func() {
			It("returns (nil, nil)", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(
							w,
							`[{"type":"file","name":"README.md","path":"README.md","size":42}]`,
						)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				content, err := client.GetFileContent(ctx, "owner", "repo", "somedir", "main")
				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(BeNil())
			})
		})
	})

	Describe("GetJobLog", func() {
		Context("GitHub API returns 302 and log server returns content", func() {
			It("fetches and returns the log bytes", func() {
				logContent := "step 1: ok\nstep 2: FAILED\n"

				// Log storage server — plain HTTP so http.DefaultClient can reach it
				logServer := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusOK)
						fmt.Fprint(w, logContent)
					}),
				)
				defer logServer.Close()

				// GitHub API mock — returns 302 redirect to logServer
				ghServer := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(r.URL.Path).To(ContainSubstring("/actions/jobs/"))
						http.Redirect(w, r, logServer.URL+"/log", http.StatusFound)
					}),
				)
				defer ghServer.Close()

				client := buildClient(ghServer)
				data, err := client.GetJobLog(ctx, "owner", "repo", 42)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).To(Equal(logContent))
			})
		})

		Context("GitHub API returns HTTP error", func() {
			It("returns a non-nil error", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusUnauthorized)
						fmt.Fprintf(w, `{"message":"Bad credentials"}`)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				_, err := client.GetJobLog(ctx, "owner", "repo", 42)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("log payload exceeds 1 MiB", func() {
			It("returns an error", func() {
				// Serve 1 MiB + 1 byte so the size check triggers
				oversizedContent := make([]byte, 1024*1024+2)
				for i := range oversizedContent {
					oversizedContent[i] = 'x'
				}

				logServer := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write(oversizedContent)
					}),
				)
				defer logServer.Close()

				ghServer := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						http.Redirect(w, r, logServer.URL+"/log", http.StatusFound)
					}),
				)
				defer ghServer.Close()

				client := buildClient(ghServer)
				_, err := client.GetJobLog(ctx, "owner", "repo", 42)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("exceeds 1 MiB"))
			})
		})
	})

	Describe("GetJobsForRun", func() {
		Context("server returns jobs with failed steps", func() {
			It("returns failed job info with correct failed step name", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						Expect(
							r.URL.Path,
						).To(ContainSubstring("/repos/owner/repo/actions/runs/42/jobs"))
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{
							"total_count": 2,
							"jobs": [
								{
									"id": 101,
									"name": "build",
									"conclusion": "failure",
									"steps": [
										{"name": "setup", "conclusion": "success"},
										{"name": "compile", "conclusion": "failure"},
										{"name": "teardown", "conclusion": "skipped"}
									]
								},
								{
									"id": 102,
									"name": "test",
									"conclusion": "failure",
									"steps": [
										{"name": "unit-tests", "conclusion": "failure"},
										{"name": "integration-tests", "conclusion": "skipped"}
									]
								}
							]
						}`)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				result, err := client.GetJobsForRun(ctx, "owner", "repo", 42)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))

				Expect(result[0].JobID).To(Equal(int64(101)))
				Expect(result[0].JobName).To(Equal("build"))
				Expect(result[0].FailedStepName).To(Equal("compile"))

				Expect(result[1].JobID).To(Equal(int64(102)))
				Expect(result[1].JobName).To(Equal("test"))
				Expect(result[1].FailedStepName).To(Equal("unit-tests"))
			})
		})

		Context("server returns only successful jobs", func() {
			It("returns an empty slice", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{
							"total_count": 2,
							"jobs": [
								{
									"id": 201,
									"name": "lint",
									"conclusion": "success",
									"steps": [
										{"name": "lint-check", "conclusion": "success"}
									]
								},
								{
									"id": 202,
									"name": "format",
									"conclusion": "success",
									"steps": [
										{"name": "fmt-check", "conclusion": "success"}
									]
								}
							]
						}`)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				result, err := client.GetJobsForRun(ctx, "owner", "repo", 42)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeEmpty())
			})
		})

		Context("server returns HTTP 401", func() {
			It("returns an error", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusUnauthorized)
						fmt.Fprintf(w, `{"message":"Bad credentials"}`)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				_, err := client.GetJobsForRun(ctx, "owner", "repo", 42)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("server returns rate limit response", func() {
			It("returns ErrRateLimited", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("X-RateLimit-Remaining", "0")
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprintf(
							w,
							`{"message": "You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`,
						)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				_, err := client.GetJobsForRun(ctx, "owner", "repo", 42)
				Expect(err).To(MatchError(pkg.ErrRateLimited))
			})
		})
	})

	Describe("ListOwnerRepos", func() {
		Context("owner is a user", func() {
			It("returns non-archived, non-fork repos", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/users/testuser":
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `{"login":"testuser","type":"User"}`)
						case "/users/testuser/repos":
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(
								w,
								`[{"name":"repo-a","archived":false,"fork":false},{"name":"repo-b","archived":true,"fork":false},{"name":"repo-c","archived":false,"fork":true},{"name":"repo-d","archived":false,"fork":false}]`,
							)
						default:
							w.WriteHeader(http.StatusNotFound)
						}
					}),
				)
				defer server.Close()

				client := buildClient(server)
				result, err := client.ListOwnerRepos(ctx, "testuser")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal([]string{"repo-a", "repo-d"}))
			})
		})

		Context("owner is an organization", func() {
			It("calls ListByOrg", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/users/testorg":
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `{"login":"testorg","type":"Organization"}`)
						case "/orgs/testorg/repos":
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `[{"name":"org-repo","archived":false,"fork":false}]`)
						default:
							w.WriteHeader(http.StatusNotFound)
						}
					}),
				)
				defer server.Close()

				client := buildClient(server)
				result, err := client.ListOwnerRepos(ctx, "testorg")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal([]string{"org-repo"}))
			})
		})

		Context("owner not found (404)", func() {
			It("returns an error", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprintf(w, `{"message":"Not Found"}`)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				_, err := client.ListOwnerRepos(ctx, "nonexistent")
				Expect(err).To(HaveOccurred())
			})
		})

		Context("rate limited during list", func() {
			It("returns ErrRateLimited", func() {
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("X-RateLimit-Remaining", "0")
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprintf(
							w,
							`{"message": "You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`,
						)
					}),
				)
				defer server.Close()

				client := buildClient(server)
				_, err := client.ListOwnerRepos(ctx, "testuser")
				Expect(err).To(MatchError(pkg.ErrRateLimited))
			})
		})

		Context("two-page response", func() {
			It("returns repos from both pages", func() {
				var serverURL string
				server := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/users/testuser":
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `{"login":"testuser","type":"User"}`)
						case "/users/testuser/repos":
							page := r.URL.Query().Get("page")
							w.Header().Set("Content-Type", "application/json")
							if page == "1" {
								w.Header().
									Set("Link", `<`+serverURL+`/users/testuser/repos?page=2>; rel="next"`)
								fmt.Fprintf(w, `[{"name":"repo-a","archived":false,"fork":false}]`)
							} else {
								fmt.Fprintf(w, `[{"name":"repo-b","archived":false,"fork":false}]`)
							}
						default:
							w.WriteHeader(http.StatusNotFound)
						}
					}),
				)
				defer server.Close()
				serverURL = server.URL

				client := buildClient(server)
				result, err := client.ListOwnerRepos(ctx, "testuser")
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal([]string{"repo-a", "repo-b"}))
			})
		})
	})
})
