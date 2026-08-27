// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import "github.com/prometheus/client_golang/prometheus"

//counterfeiter:generate -o ../mocks/metrics.go --fake-name Metrics . Metrics

// Metrics defines Prometheus counters and gauges for the build watcher.
type Metrics interface {
	// IncPollCycle increments the poll cycle counter.
	// result: "success" | "error"
	IncPollCycle(result string)
	// IncReposChecked increments the repos-checked counter for each repo polled.
	IncReposChecked()
	// IncStateTransition increments the state-transition counter.
	// transition: "green_to_red" | "red_to_green"
	IncStateTransition(transition string)
	// IncTaskPublished increments the published-task counter.
	IncTaskPublished()
	// IncTaskClosed increments the published-closure counter.
	IncTaskClosed()
	// IncPollError increments the poll-error counter.
	// reason: "rate_limited" | "github_error" | "kafka_error"
	IncPollError(reason string)
	// SetCurrentRedRepos sets the gauge to the current number of repos in red state.
	SetCurrentRedRepos(count float64)
	// SetRateLimitRemaining records the primary rate-limit window's remaining
	// for the shared GitHub App installation token, as read from the last
	// core-bucket API response's X-RateLimit-Remaining header.
	SetRateLimitRemaining(remaining int)
	// IncWebhookDelivery increments the webhook delivery counter with the given result label.
	// result: "success", "skip"
	IncWebhookDelivery(result string)
	// IncWebhookSignatureRejected increments the webhook signature-rejection counter.
	IncWebhookSignatureRejected()
	// ObserveWebhookDispatchLatency records the dispatch latency of a webhook delivery.
	ObserveWebhookDispatchLatency(seconds float64)
	// IncWebhookSkipped increments the webhook skip counter.
	// reason: "not_completed" | "not_failure" | "not_default_branch" | "debounced"
	IncWebhookSkipped(reason string)
}

var (
	buildPollCyclesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "github_build_watcher_poll_cycles_total",
		Help: "Total number of poll cycles by result.",
	}, []string{"result"})

	buildReposCheckedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "github_build_watcher_repos_checked_total",
		Help: "Total number of repos checked across all poll cycles.",
	})

	buildStateTransitionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "github_build_watcher_state_transitions_total",
		Help: "Total number of build state transitions by type.",
	}, []string{"transition"})

	buildTasksPublishedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "github_build_watcher_tasks_published_total",
		Help: "Total number of CreateTaskCommands published to Kafka.",
	})

	buildTasksClosedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "github_build_watcher_tasks_closed_total",
		Help: "Total number of CompleteCommands published to Kafka (red→green closures).",
	})

	buildPollErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "github_build_watcher_poll_errors_total",
		Help: "Total number of poll errors by reason.",
	}, []string{"reason"})

	buildCurrentRedRepos = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "github_build_watcher_current_red_repos",
		Help: "Current number of repositories in red (failing build) state.",
	})

	buildRateLimitRemaining = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "github_build_watcher_rate_limit_remaining",
		Help: "Remaining requests in the shared GitHub App installation token's primary rate-limit window (core bucket), as read from the last API response's X-RateLimit-Remaining header.",
	})

	buildWebhookDeliveriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "github_build_watcher_webhook_deliveries_total",
		Help: "Total GitHub webhook deliveries by result.",
	}, []string{"result"})

	buildWebhookSignatureRejectionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "github_build_watcher_webhook_signature_rejections_total",
		Help: "Total GitHub webhook payloads rejected for an invalid HMAC signature.",
	})

	buildWebhookDispatchLatencySeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "github_build_watcher_webhook_dispatch_latency_seconds",
		Help:    "Latency of dispatching a GitHub webhook delivery to Kafka.",
		Buckets: prometheus.DefBuckets,
	})

	buildWebhookSkippedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "github_build_watcher_webhook_skipped_total",
		Help: "Total GitHub webhook workflow_run deliveries skipped by reason.",
	}, []string{"reason"})
)

func init() {
	prometheus.MustRegister(
		buildPollCyclesTotal,
		buildReposCheckedTotal,
		buildStateTransitionsTotal,
		buildTasksPublishedTotal,
		buildTasksClosedTotal,
		buildPollErrorsTotal,
		buildCurrentRedRepos,
		buildRateLimitRemaining,
		buildWebhookDeliveriesTotal,
		buildWebhookSignatureRejectionsTotal,
		buildWebhookDispatchLatencySeconds,
		buildWebhookSkippedTotal,
	)
	for _, result := range []string{"success", "error"} {
		buildPollCyclesTotal.WithLabelValues(result).Add(0)
	}
	for _, transition := range []string{"green_to_red", "red_to_green"} {
		buildStateTransitionsTotal.WithLabelValues(transition).Add(0)
	}
	for _, reason := range []string{"rate_limited", "github_error", "kafka_error"} {
		buildPollErrorsTotal.WithLabelValues(reason).Add(0)
	}
	for _, result := range []string{"success", "skip"} {
		buildWebhookDeliveriesTotal.WithLabelValues(result).Add(0)
	}
	for _, reason := range []string{"not_completed", "not_failure", "not_default_branch", "debounced"} {
		buildWebhookSkippedTotal.WithLabelValues(reason).Add(0)
	}
}

type buildPrometheusMetrics struct{}

// NewMetrics returns a Metrics implementation backed by Prometheus counters.
func NewMetrics() Metrics {
	return &buildPrometheusMetrics{}
}

func (m *buildPrometheusMetrics) IncPollCycle(result string) {
	buildPollCyclesTotal.WithLabelValues(result).Inc()
}

func (m *buildPrometheusMetrics) IncReposChecked() {
	buildReposCheckedTotal.Inc()
}

func (m *buildPrometheusMetrics) IncStateTransition(transition string) {
	buildStateTransitionsTotal.WithLabelValues(transition).Inc()
}

func (m *buildPrometheusMetrics) IncTaskPublished() {
	buildTasksPublishedTotal.Inc()
}

func (m *buildPrometheusMetrics) IncTaskClosed() {
	buildTasksClosedTotal.Inc()
}

func (m *buildPrometheusMetrics) IncPollError(reason string) {
	buildPollErrorsTotal.WithLabelValues(reason).Inc()
}

func (m *buildPrometheusMetrics) SetCurrentRedRepos(count float64) {
	buildCurrentRedRepos.Set(count)
}

func (m *buildPrometheusMetrics) SetRateLimitRemaining(remaining int) {
	buildRateLimitRemaining.Set(float64(remaining))
}

func (m *buildPrometheusMetrics) IncWebhookDelivery(result string) {
	buildWebhookDeliveriesTotal.WithLabelValues(result).Inc()
}

func (m *buildPrometheusMetrics) IncWebhookSignatureRejected() {
	buildWebhookSignatureRejectionsTotal.Inc()
}

func (m *buildPrometheusMetrics) ObserveWebhookDispatchLatency(seconds float64) {
	buildWebhookDispatchLatencySeconds.Observe(seconds)
}

func (m *buildPrometheusMetrics) IncWebhookSkipped(reason string) {
	buildWebhookSkippedTotal.WithLabelValues(reason).Inc()
}
