// Command chronos-worker hosts the sample order-fulfillment workflow and its
// activities. Run multiple instances to scale horizontally; they cooperate via
// the durable task queues in Postgres.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/AymanYouss/chronos-engine/internal/config"
	"github.com/AymanYouss/chronos-engine/internal/metrics"
	"github.com/AymanYouss/chronos-engine/internal/version"
	"github.com/AymanYouss/chronos-engine/sdk/client"
	"github.com/AymanYouss/chronos-engine/sdk/worker"
	"github.com/AymanYouss/chronos-engine/workflows"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("worker exited", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.LoadWorker()
	if err != nil {
		return err
	}
	logger.Info("starting chronos-worker",
		"version", version.Version, "server", cfg.ServerAddr, "task_queue", cfg.TaskQueue)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	c, err := connectWithRetry(ctx, cfg.ServerAddr, logger)
	if err != nil {
		return err
	}
	defer c.Close()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := metrics.New(reg)
	go serveMetrics(cfg.MetricsAddr, reg, logger)

	w := worker.New(c, worker.Options{
		TaskQueue:   cfg.TaskQueue,
		Identity:    cfg.Identity,
		Concurrency: cfg.Concurrency,
		Logger:      logger,
		Metrics:     m,
	})

	activities := workflows.NewActivities(workflows.NewLedger(pool))
	activities.Register(w)

	return w.Run(ctx)
}

// connectWithRetry dials the control plane, retrying so a worker started
// alongside the server (e.g. in docker-compose) waits for it to come up.
func connectWithRetry(ctx context.Context, addr string, logger *slog.Logger) (*client.Client, error) {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		c, err := client.Dial(addr)
		if err == nil {
			return c, nil
		}
		lastErr = err
		logger.Info("waiting for control plane", "addr", addr, "attempt", attempt+1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, lastErr
}

func serveMetrics(addr string, reg *prometheus.Registry, logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Warn("metrics server error", "error", err)
	}
}
