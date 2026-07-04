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
	// IncPollError increments the poll-error counter.
	// reason: "rate_limited" | "github_error" | "kafka_error"
	IncPollError(reason string)
	// SetCurrentRedRepos sets the gauge to the current number of repos in red state.
	SetCurrentRedRepos(count float64)
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

	buildPollErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "github_build_watcher_poll_errors_total",
		Help: "Total number of poll errors by reason.",
	}, []string{"reason"})

	buildCurrentRedRepos = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "github_build_watcher_current_red_repos",
		Help: "Current number of repositories in red (failing build) state.",
	})
)

func init() {
	prometheus.MustRegister(
		buildPollCyclesTotal,
		buildReposCheckedTotal,
		buildStateTransitionsTotal,
		buildTasksPublishedTotal,
		buildPollErrorsTotal,
		buildCurrentRedRepos,
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

func (m *buildPrometheusMetrics) IncPollError(reason string) {
	buildPollErrorsTotal.WithLabelValues(reason).Inc()
}

func (m *buildPrometheusMetrics) SetCurrentRedRepos(count float64) {
	buildCurrentRedRepos.Set(count)
}
