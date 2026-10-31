// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// GitHub rate-limit buckets. Each is an independent quota: core is the
// installation token's hourly budget, search is 30/min, graphql is metered in
// points.
const (
	GitHubAPIBucketCore    = "core"
	GitHubAPIBucketSearch  = "search"
	GitHubAPIBucketGraphQL = "graphql"
)

// GitHubAPIClassUnclassified is the only rate-limit class emitted until a
// classification rule separating primary exhaustion from secondary rate
// limiting exists. The label is always present; no other value is guessed.
const GitHubAPIClassUnclassified = "unclassified"

const (
	githubAPIOutcomeSuccess = "success"
	githubAPIOutcomeError   = "error"
)

var (
	githubAPIBuckets  = []string{GitHubAPIBucketCore, GitHubAPIBucketSearch, GitHubAPIBucketGraphQL}
	githubAPIOutcomes = []string{githubAPIOutcomeSuccess, githubAPIOutcomeError}
	githubAPIClasses  = []string{GitHubAPIClassUnclassified}

	githubAPICallsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "github_api_calls_total",
		Help: "GitHub API calls by calling service, credential (App id), rate-limit bucket derived from the request path, outcome and cluster.",
	}, []string{"service", "credential", "bucket", "outcome", "cluster"})

	githubAPIRateLimitedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "github_api_rate_limited_total",
		Help: "GitHub API responses that were rate limited (429, or 403 with exhausted quota or Retry-After), by calling service, credential, bucket, class and cluster.",
	}, []string{"service", "credential", "bucket", "class", "cluster"})
)

func init() {
	prometheus.MustRegister(githubAPICallsTotal, githubAPIRateLimitedTotal)
}

// GitHubAPILabels are the per-process label values stamped on every GitHub
// API counter. Credential carries the App id, never a token.
type GitHubAPILabels struct {
	Service    string
	Credential string
	Cluster    string
}

//counterfeiter:generate -o ../mocks/github_api_recorder.go --fake-name GitHubAPIRecorder . GitHubAPIRecorder

// GitHubAPIRecorder counts one GitHub API round trip.
type GitHubAPIRecorder interface {
	// Record counts the round trip of req. resp is nil and err non-nil on a
	// transport error.
	Record(req *http.Request, resp *http.Response, err error)
}

// NewGitHubAPIRecorder returns a recorder that stamps labels on every count.
// Every bounded label combination is pre-initialised to zero so rate() and
// absent() queries see the series before the first call of that kind.
func NewGitHubAPIRecorder(labels GitHubAPILabels) GitHubAPIRecorder {
	for _, bucket := range githubAPIBuckets {
		for _, outcome := range githubAPIOutcomes {
			githubAPICallsTotal.WithLabelValues(labels.Service, labels.Credential, bucket, outcome, labels.Cluster).
				Add(0)
		}
		for _, class := range githubAPIClasses {
			githubAPIRateLimitedTotal.WithLabelValues(labels.Service, labels.Credential, bucket, class, labels.Cluster).
				Add(0)
		}
	}
	return &githubAPIRecorder{labels: labels}
}

type githubAPIRecorder struct {
	labels GitHubAPILabels
}

func (r *githubAPIRecorder) Record(req *http.Request, resp *http.Response, err error) {
	if !IsGitHubAPIRequest(req) {
		return
	}
	bucket := GitHubAPIBucket(req)
	outcome := githubAPIOutcomeSuccess
	if err != nil || resp == nil || resp.StatusCode >= http.StatusBadRequest {
		outcome = githubAPIOutcomeError
	}
	githubAPICallsTotal.WithLabelValues(r.labels.Service, r.labels.Credential, bucket, outcome, r.labels.Cluster).
		Inc()
	if err == nil && IsGitHubRateLimited(resp) {
		githubAPIRateLimitedTotal.WithLabelValues(r.labels.Service, r.labels.Credential, bucket, GitHubAPIClassUnclassified, r.labels.Cluster).
			Inc()
	}
}

// IsGitHubAPIRequest reports whether req targets the GitHub API and so spends
// quota. Redirect hops to other hosts (e.g. job logs served from Azure blob
// storage) share the transport but spend no GitHub quota and are not counted.
func IsGitHubAPIRequest(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	if req.URL.Hostname() == "api.github.com" {
		return true
	}
	// GitHub Enterprise Server serves the API under /api/v3 and /api/graphql.
	return strings.HasPrefix(req.URL.Path, "/api/v3/") || req.URL.Path == "/api/graphql"
}

// GitHubAPIBucket derives the rate-limit bucket from the request path. It never
// reads a response: the response's X-RateLimit headers describe whichever
// bucket answered, which is how a search response once masqueraded as core.
func GitHubAPIBucket(req *http.Request) string {
	if req == nil || req.URL == nil {
		return GitHubAPIBucketCore
	}
	path := strings.TrimPrefix(req.URL.Path, "/api/v3")
	switch {
	case strings.HasPrefix(path, "/search/"):
		return GitHubAPIBucketSearch
	case path == "/graphql" || req.URL.Path == "/api/graphql":
		return GitHubAPIBucketGraphQL
	default:
		return GitHubAPIBucketCore
	}
}

// IsGitHubRateLimited reports whether resp is a rate-limit rejection: a 429,
// or a 403 carrying an exhausted X-RateLimit-Remaining or a Retry-After.
func IsGitHubRateLimited(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return true
	case http.StatusForbidden:
		return resp.Header.Get("X-RateLimit-Remaining") == "0" ||
			resp.Header.Get("Retry-After") != ""
	default:
		return false
	}
}
