// Command chronos-server runs the Chronos control plane: the workflow gRPC
// service, the inspector REST API, the durable timer service, and the
// Prometheus metrics endpoint.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/internal/config"
	"github.com/AymanYouss/chronos-engine/internal/httpapi"
	"github.com/AymanYouss/chronos-engine/internal/metrics"
	"github.com/AymanYouss/chronos-engine/internal/server"
	"github.com/AymanYouss/chronos-engine/internal/storage/postgres"
	"github.com/AymanYouss/chronos-engine/internal/version"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.LoadServer()
	if err != nil {
		return err
	}
	logger.Info("starting chronos-server", "version", version.Version, "grpc", cfg.GRPCAddr, "http", cfg.HTTPAddr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		return err
	}
	logger.Info("database migrations applied")

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := metrics.New(reg)

	svc := server.New(store, m, server.Options{Lease: cfg.TaskLease, PollWait: cfg.PollWait})

	// gRPC control plane.
	grpcServer := grpc.NewServer(grpc.MaxConcurrentStreams(1000))
	commonv1.RegisterWorkflowServiceServer(grpcServer, svc)
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("chronos.v1.WorkflowService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	reflection.Register(grpcServer)

	grpcLis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}

	// Inspector REST API (+ optional static UI).
	api := httpapi.New(store, svc, cfg.UIAssetsDir)
	httpServer := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second}

	// Metrics + health.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := store.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	metricsServer := &http.Server{Addr: cfg.MetricsAddr, Handler: metricsMux, ReadHeaderTimeout: 10 * time.Second}

	errCh := make(chan error, 3)
	go func() {
		logger.Info("gRPC listening", "addr", cfg.GRPCAddr)
		errCh <- grpcServer.Serve(grpcLis)
	}()
	go func() {
		logger.Info("HTTP API listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go func() {
		logger.Info("metrics listening", "addr", cfg.MetricsAddr)
		if err := metricsServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go runTimerService(ctx, store, m, cfg.TimerInterval, logger)
	go runStatsUpdater(ctx, store, m, logger)

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		logger.Error("server error", "error", err)
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	grpcServer.GracefulStop()
	_ = httpServer.Shutdown(shutdownCtx)
	_ = metricsServer.Shutdown(shutdownCtx)
	logger.Info("shutdown complete")
	return nil
}

// runTimerService periodically fires due durable timers.
func runTimerService(ctx context.Context, store *postgres.Store, m *metrics.Metrics, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fired, err := store.FireDueTimers(ctx, time.Now(), 500)
			if err != nil {
				logger.Warn("timer service error", "error", err)
				continue
			}
			if fired > 0 {
				m.TimersFired.Add(float64(fired))
			}
		}
	}
}

// runStatsUpdater periodically refreshes the executions-by-status gauges.
func runStatsUpdater(ctx context.Context, store *postgres.Store, m *metrics.Metrics, logger *slog.Logger) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	update := func() {
		counts, err := store.CountByStatus(ctx)
		if err != nil {
			logger.Warn("stats updater error", "error", err)
			return
		}
		for status, n := range counts {
			m.ExecutionsByStatus.WithLabelValues(statusLabel(status)).Set(float64(n))
		}
	}
	update()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			update()
		}
	}
}

func statusLabel(s commonv1.WorkflowStatus) string {
	switch s {
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_RUNNING:
		return "running"
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_COMPLETED:
		return "completed"
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_FAILED:
		return "failed"
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_TIMED_OUT:
		return "timed_out"
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_TERMINATED:
		return "terminated"
	default:
		return "unspecified"
	}
}
