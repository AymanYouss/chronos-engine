// Package metrics defines the Prometheus collectors exported by the control
// plane and workers.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics bundles every collector so it can be injected and reused.
type Metrics struct {
	WorkflowsStarted   prometheus.Counter
	WorkflowsCompleted prometheus.Counter
	WorkflowsFailed    prometheus.Counter
	ActivitiesStarted  prometheus.Counter
	ActivitiesComplete prometheus.Counter
	ActivitiesFailed   prometheus.Counter
	ActivitiesRetried  prometheus.Counter
	TimersFired        prometheus.Counter

	WorkflowTasksPolled prometheus.Counter
	ActivityTasksPolled prometheus.Counter

	RPCLatency    *prometheus.HistogramVec
	ReplayLatency prometheus.Histogram

	ExecutionsByStatus *prometheus.GaugeVec
}

// New registers all collectors against the given registry and returns the set.
func New(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	return &Metrics{
		WorkflowsStarted: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_workflows_started_total",
			Help: "Total workflow executions started.",
		}),
		WorkflowsCompleted: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_workflows_completed_total",
			Help: "Total workflow executions that completed successfully.",
		}),
		WorkflowsFailed: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_workflows_failed_total",
			Help: "Total workflow executions that terminated with failure.",
		}),
		ActivitiesStarted: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_activities_started_total",
			Help: "Total activity task attempts dispatched to workers.",
		}),
		ActivitiesComplete: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_activities_completed_total",
			Help: "Total activity tasks recorded as completed.",
		}),
		ActivitiesFailed: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_activities_failed_total",
			Help: "Total activity tasks that failed terminally.",
		}),
		ActivitiesRetried: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_activities_retried_total",
			Help: "Total activity task retries scheduled with backoff.",
		}),
		TimersFired: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_timers_fired_total",
			Help: "Total durable timers fired.",
		}),
		WorkflowTasksPolled: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_workflow_tasks_polled_total",
			Help: "Total workflow tasks handed to workers.",
		}),
		ActivityTasksPolled: factory.NewCounter(prometheus.CounterOpts{
			Name: "chronos_activity_tasks_polled_total",
			Help: "Total activity tasks handed to workers.",
		}),
		RPCLatency: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "chronos_rpc_duration_seconds",
			Help:    "Control-plane RPC latency by method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "code"}),
		ReplayLatency: factory.NewHistogram(prometheus.HistogramOpts{
			Name:    "chronos_replay_duration_seconds",
			Help:    "Time to deterministically replay a workflow history.",
			Buckets: []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1},
		}),
		ExecutionsByStatus: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "chronos_executions",
			Help: "Current number of executions by status.",
		}, []string{"status"}),
	}
}
