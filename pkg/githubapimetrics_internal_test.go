// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// roundTripFunc adapts a func to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// apiResponse builds a response carrying the bucket header GitHub would send.
func apiResponse(status int, resource string) *http.Response {
	h := http.Header{}
	if resource != "" {
		h.Set("X-RateLimit-Resource", resource)
	}
	return &http.Response{StatusCode: status, Header: h, Body: http.NoBody}
}

var testLabelSeq atomic.Int64

// freshLabels returns labels unique to one test, so the process-global
// counters never carry counts from another test.
func freshLabels() GitHubAPILabels {
	return GitHubAPILabels{
		Service:    fmt.Sprintf("test-service-%d", testLabelSeq.Add(1)),
		Credential: "4355244",
		Cluster:    "nuke",
	}
}

func calls(l GitHubAPILabels, bucket, outcome string) float64 {
	return testutil.ToFloat64(
		githubAPICallsTotal.WithLabelValues(l.Service, l.Credential, bucket, outcome, l.Cluster),
	)
}

func rateLimited(l GitHubAPILabels, bucket string) float64 {
	return testutil.ToFloat64(
		githubAPIRateLimitedTotal.WithLabelValues(
			l.Service,
			l.Credential,
			bucket,
			GitHubAPIClassUnclassified,
			l.Cluster,
		),
	)
}

func doRequest(tr http.RoundTripper, url string) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	Expect(err).NotTo(HaveOccurred())
	resp, err := tr.RoundTrip(req)
	if err == nil && resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

var _ = Describe("GitHubAPIBucket", func() {
	DescribeTable(
		"derives the bucket from the request path",
		func(url, expected string) {
			req, err := http.NewRequest(http.MethodGet, url, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(GitHubAPIBucket(req)).To(Equal(expected))
		},
		Entry("search issues", "https://api.github.com/search/issues?q=x", GitHubAPIBucketSearch),
		Entry("search code", "https://api.github.com/search/code", GitHubAPIBucketSearch),
		Entry("graphql", "https://api.github.com/graphql", GitHubAPIBucketGraphQL),
		Entry("repos", "https://api.github.com/repos/o/r/actions/runs", GitHubAPIBucketCore),
		Entry("rate_limit", "https://api.github.com/rate_limit", GitHubAPIBucketCore),
		Entry(
			"path merely containing search",
			"https://api.github.com/repos/o/search/contents",
			GitHubAPIBucketCore,
		),
		Entry("GHE search", "https://ghe.example.com/api/v3/search/issues", GitHubAPIBucketSearch),
		Entry("GHE graphql", "https://ghe.example.com/api/graphql", GitHubAPIBucketGraphQL),
		Entry("GHE core", "https://ghe.example.com/api/v3/repos/o/r", GitHubAPIBucketCore),
	)
})

var _ = Describe("IsGitHubAPIRequest", func() {
	DescribeTable(
		"counts only requests that spend GitHub quota",
		func(url string, expected bool) {
			req, err := http.NewRequest(http.MethodGet, url, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(IsGitHubAPIRequest(req)).To(Equal(expected))
		},
		Entry("api.github.com", "https://api.github.com/repos/o/r", true),
		Entry("GHE v3", "https://ghe.example.com/api/v3/repos/o/r", true),
		Entry(
			"job-log redirect to blob storage",
			"https://pipelines.blob.core.windows.net/logs/1.txt",
			false,
		),
	)
})

var _ = Describe("IsGitHubRateLimited", func() {
	It("treats 429 as rate limited", func() {
		Expect(IsGitHubRateLimited(apiResponse(http.StatusTooManyRequests, "core"))).To(BeTrue())
	})
	It("treats 403 with exhausted quota as rate limited", func() {
		resp := apiResponse(http.StatusForbidden, "core")
		resp.Header.Set("X-RateLimit-Remaining", "0")
		Expect(IsGitHubRateLimited(resp)).To(BeTrue())
	})
	It("treats 403 with Retry-After as rate limited", func() {
		resp := apiResponse(http.StatusForbidden, "core")
		resp.Header.Set("Retry-After", "60")
		Expect(IsGitHubRateLimited(resp)).To(BeTrue())
	})
	It("does not treat a permission 403 as rate limited", func() {
		resp := apiResponse(http.StatusForbidden, "core")
		resp.Header.Set("X-RateLimit-Remaining", "4999")
		Expect(IsGitHubRateLimited(resp)).To(BeFalse())
	})
	It("does not treat 200 as rate limited", func() {
		Expect(IsGitHubRateLimited(apiResponse(http.StatusOK, "core"))).To(BeFalse())
	})
})

var _ = Describe("rateCapturingTransport per-caller counters", func() {
	var labels GitHubAPILabels

	BeforeEach(func() {
		labels = freshLabels()
	})

	// Gathers from the registry without calling WithLabelValues, which would
	// create the series on read and make the assertion vacuous.
	It("pre-initialises every bucket x outcome and bucket x class at zero", func() {
		NewGitHubAPIRecorder(labels)

		families, err := prometheus.DefaultGatherer.Gather()
		Expect(err).NotTo(HaveOccurred())
		counts := map[string]int{}
		for _, mf := range families {
			for _, m := range mf.GetMetric() {
				for _, lp := range m.GetLabel() {
					if lp.GetName() == "service" && lp.GetValue() == labels.Service {
						Expect(m.GetCounter().GetValue()).To(Equal(0.0))
						counts[mf.GetName()]++
					}
				}
			}
		}
		Expect(counts).To(Equal(map[string]int{
			"github_api_calls_total":        6,
			"github_api_rate_limited_total": 3,
		}))
	})

	// The inverse of the recorded defect: a gauge fed by whichever response
	// arrived last. Each call must be counted under its own request's bucket,
	// whatever bucket the previous response announced.
	It("labels interleaved search and core calls by request, not by response", func() {
		tr := &rateCapturingTransport{
			inner: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return apiResponse(http.StatusOK, GitHubAPIBucket(req)), nil
			}),
			set:      func(int) {},
			recorder: NewGitHubAPIRecorder(labels),
		}

		doRequest(tr, "https://api.github.com/search/issues?q=a")
		doRequest(tr, "https://api.github.com/repos/o/r/actions/runs")
		doRequest(tr, "https://api.github.com/search/issues?q=b")

		Expect(calls(labels, GitHubAPIBucketSearch, "success")).To(Equal(2.0))
		Expect(calls(labels, GitHubAPIBucketCore, "success")).To(Equal(1.0))
		Expect(calls(labels, GitHubAPIBucketGraphQL, "success")).To(Equal(0.0))
	})

	It("ignores a response's X-RateLimit-Resource when choosing the bucket", func() {
		tr := &rateCapturingTransport{
			inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return apiResponse(http.StatusOK, GitHubAPIBucketSearch), nil
			}),
			set:      func(int) {},
			recorder: NewGitHubAPIRecorder(labels),
		}

		doRequest(tr, "https://api.github.com/repos/o/r")

		Expect(calls(labels, GitHubAPIBucketCore, "success")).To(Equal(1.0))
		Expect(calls(labels, GitHubAPIBucketSearch, "success")).To(Equal(0.0))
	})

	It("counts a 429 as an error and a rate-limited event with class unclassified", func() {
		tr := &rateCapturingTransport{
			inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return apiResponse(http.StatusTooManyRequests, "core"), nil
			}),
			set:      func(int) {},
			recorder: NewGitHubAPIRecorder(labels),
		}

		doRequest(tr, "https://api.github.com/repos/o/r")

		Expect(calls(labels, GitHubAPIBucketCore, "error")).To(Equal(1.0))
		Expect(rateLimited(labels, GitHubAPIBucketCore)).To(Equal(1.0))
	})

	It("counts a transport error as an error", func() {
		tr := &rateCapturingTransport{
			inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, stderrors.New("connection reset")
			}),
			set:      func(int) {},
			recorder: NewGitHubAPIRecorder(labels),
		}

		doRequest(tr, "https://api.github.com/repos/o/r")

		Expect(calls(labels, GitHubAPIBucketCore, "error")).To(Equal(1.0))
		Expect(rateLimited(labels, GitHubAPIBucketCore)).To(Equal(0.0))
	})

	It("does not count a redirect hop to a non-GitHub host", func() {
		tr := &rateCapturingTransport{
			inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return apiResponse(http.StatusOK, ""), nil
			}),
			set:      func(int) {},
			recorder: NewGitHubAPIRecorder(labels),
		}

		doRequest(tr, "https://pipelines.blob.core.windows.net/logs/1.txt")

		Expect(calls(labels, GitHubAPIBucketCore, "success")).To(Equal(0.0))
	})

	It("tolerates a nil recorder", func() {
		tr := &rateCapturingTransport{
			inner: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return apiResponse(http.StatusOK, "core"), nil
			}),
			set: func(int) {},
		}
		Expect(func() { doRequest(tr, "https://api.github.com/repos/o/r") }).NotTo(Panic())
	})
})
