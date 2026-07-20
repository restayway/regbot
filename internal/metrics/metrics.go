// Package metrics defines low-cardinality Prometheus metrics for Regbot.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/restayway/regbot/pkg/plan"
)

type Metrics struct {
	Runs       *prometheus.CounterVec
	Artifacts  *prometheus.CounterVec
	RunSeconds prometheus.Histogram
}

func New(registerer prometheus.Registerer) *Metrics {
	metrics := &Metrics{
		Runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "regbot_runs_total", Help: "Completed Regbot runs.",
		}, []string{"result"}),
		Artifacts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "regbot_artifacts_total", Help: "Artifacts processed by Regbot.",
		}, []string{"result"}),
		RunSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "regbot_run_duration_seconds", Help: "Regbot run duration.", Buckets: prometheus.DefBuckets,
		}),
	}
	registerer.MustRegister(metrics.Runs, metrics.Artifacts, metrics.RunSeconds)
	return metrics
}

func (m *Metrics) Observe(result plan.Result, err error) {
	status := "success"
	if err != nil {
		status = "failure"
	}
	m.Runs.WithLabelValues(status).Inc()
	m.Artifacts.WithLabelValues("planned").Add(float64(result.Planned))
	m.Artifacts.WithLabelValues("deleted").Add(float64(result.Deleted))
	m.Artifacts.WithLabelValues("skipped").Add(float64(result.Skipped))
	m.Artifacts.WithLabelValues("failed").Add(float64(result.Failed))
	m.RunSeconds.Observe(result.FinishedAt.Sub(result.StartedAt).Seconds())
}
