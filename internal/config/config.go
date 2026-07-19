// Package config loads control-plane and worker configuration from the
// environment with sane production defaults.
package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Server holds control-plane configuration.
type Server struct {
	// DatabaseURL is the Postgres DSN, e.g. postgres://user:pass@host:5432/chronos.
	DatabaseURL string `envconfig:"DATABASE_URL" default:"postgres://chronos:chronos@localhost:5432/chronos?sslmode=disable"`
	// GRPCAddr is the listen address for the workflow gRPC service.
	GRPCAddr string `envconfig:"GRPC_ADDR" default:":7233"`
	// HTTPAddr serves the inspector REST API and static UI assets.
	HTTPAddr string `envconfig:"HTTP_ADDR" default:":8080"`
	// MetricsAddr serves Prometheus metrics and health probes.
	MetricsAddr string `envconfig:"METRICS_ADDR" default:":9090"`
	// TaskLease is how long a polled task is invisible before redelivery.
	TaskLease time.Duration `envconfig:"TASK_LEASE" default:"30s"`
	// TimerInterval is how often the timer service scans for due timers.
	TimerInterval time.Duration `envconfig:"TIMER_INTERVAL" default:"1s"`
	// PollWait is the maximum long-poll duration before returning empty.
	PollWait time.Duration `envconfig:"POLL_WAIT" default:"5s"`
	// UIAssetsDir optionally serves a built web UI from disk.
	UIAssetsDir string `envconfig:"UI_ASSETS_DIR" default:""`
}

// Worker holds worker configuration.
type Worker struct {
	// ServerAddr is the gRPC address of the control plane.
	ServerAddr string `envconfig:"CHRONOS_SERVER" default:"localhost:7233"`
	// TaskQueue is the queue this worker polls.
	TaskQueue string `envconfig:"TASK_QUEUE" default:"default"`
	// Identity uniquely names this worker for lease ownership and history.
	Identity string `envconfig:"WORKER_IDENTITY" default:""`
	// Concurrency is the number of parallel pollers per task type.
	Concurrency int `envconfig:"WORKER_CONCURRENCY" default:"8"`
	// MetricsAddr serves worker Prometheus metrics.
	MetricsAddr string `envconfig:"METRICS_ADDR" default:":9091"`
	// DatabaseURL is used only by the sample activities' idempotency ledger.
	DatabaseURL string `envconfig:"DATABASE_URL" default:"postgres://chronos:chronos@localhost:5432/chronos?sslmode=disable"`
}

// LoadServer reads server config from the environment.
func LoadServer() (Server, error) {
	var c Server
	err := envconfig.Process("chronos", &c)
	return c, err
}

// LoadWorker reads worker config from the environment.
func LoadWorker() (Worker, error) {
	var c Worker
	err := envconfig.Process("chronos", &c)
	return c, err
}
