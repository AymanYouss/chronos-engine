// Package worker hosts workflow and activity code. Workers are stateless and
// horizontally scalable: each one long-polls the control plane for tasks,
// deterministically replays workflow histories, and executes activities. Add
// more worker processes to increase throughput; the durable task queues in
// Postgres fan work out across them safely.
package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/status"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/internal/metrics"
	"github.com/AymanYouss/chronos-engine/sdk/activity"
	"github.com/AymanYouss/chronos-engine/sdk/client"
	"github.com/AymanYouss/chronos-engine/sdk/workflow"
)

// Options configures a worker.
type Options struct {
	TaskQueue   string
	Identity    string
	Concurrency int
	Logger      *slog.Logger
	Metrics     *metrics.Metrics
}

// Worker polls a task queue and runs registered workflows and activities.
type Worker struct {
	svc         commonv1.WorkflowServiceClient
	taskQueue   string
	identity    string
	concurrency int
	logger      *slog.Logger
	metrics     *metrics.Metrics

	mu         sync.RWMutex
	workflows  map[string]workflow.Definition
	activities map[string]activity.Definition
}

// New builds a worker bound to a control-plane client.
func New(c *client.Client, opts Options) *Worker {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if opts.TaskQueue == "" {
		opts.TaskQueue = "default"
	}
	if opts.Identity == "" {
		opts.Identity = fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Worker{
		svc:         c.Service(),
		taskQueue:   opts.TaskQueue,
		identity:    opts.Identity,
		concurrency: opts.Concurrency,
		logger:      opts.Logger,
		metrics:     opts.Metrics,
		workflows:   map[string]workflow.Definition{},
		activities:  map[string]activity.Definition{},
	}
}

// RegisterWorkflow registers a workflow implementation under a type name.
func (w *Worker) RegisterWorkflow(name string, def workflow.Definition) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.workflows[name] = def
}

// RegisterActivity registers an activity implementation under a type name.
func (w *Worker) RegisterActivity(name string, def activity.Definition) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.activities[name] = def
}

// Run starts the pollers and blocks until the context is canceled.
func (w *Worker) Run(ctx context.Context) error {
	w.logger.Info("worker starting",
		"identity", w.identity, "task_queue", w.taskQueue, "concurrency", w.concurrency)

	var wg sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); w.pollWorkflowLoop(ctx) }()
		go func() { defer wg.Done(); w.pollActivityLoop(ctx) }()
	}
	wg.Wait()
	w.logger.Info("worker stopped", "identity", w.identity)
	return nil
}

func (w *Worker) pollWorkflowLoop(ctx context.Context) {
	for ctx.Err() == nil {
		resp, err := w.svc.PollWorkflowTask(ctx, &commonv1.PollWorkflowTaskRequest{
			TaskQueue: w.taskQueue, Identity: w.identity,
		})
		if err != nil {
			w.backoff(ctx, err)
			continue
		}
		if resp.TaskToken == 0 {
			continue // empty long-poll
		}
		w.handleWorkflowTask(ctx, resp)
	}
}

func (w *Worker) handleWorkflowTask(ctx context.Context, task *commonv1.PollWorkflowTaskResponse) {
	w.mu.RLock()
	def, ok := w.workflows[task.WorkflowType]
	w.mu.RUnlock()
	if !ok {
		w.logger.Warn("no workflow registered", "type", task.WorkflowType)
		return // lease expiry redelivers to a worker that has it registered
	}

	start := time.Now()
	result, err := workflow.Execute(def, w.taskQueue, task.History, w.logger)
	if w.metrics != nil {
		w.metrics.ReplayLatency.Observe(time.Since(start).Seconds())
	}
	if err != nil {
		w.logger.Error("workflow replay error", "type", task.WorkflowType, "error", err)
		return
	}

	if _, err := w.svc.RespondWorkflowTaskCompleted(ctx, &commonv1.RespondWorkflowTaskCompletedRequest{
		TaskToken: task.TaskToken, Identity: w.identity, Commands: result.Commands,
	}); err != nil {
		w.logger.Warn("respond workflow task failed", "error", err)
	}
}

func (w *Worker) pollActivityLoop(ctx context.Context) {
	for ctx.Err() == nil {
		resp, err := w.svc.PollActivityTask(ctx, &commonv1.PollActivityTaskRequest{
			TaskQueue: w.taskQueue, Identity: w.identity,
		})
		if err != nil {
			w.backoff(ctx, err)
			continue
		}
		if resp.TaskToken == 0 {
			continue
		}
		w.handleActivityTask(ctx, resp)
	}
}

func (w *Worker) handleActivityTask(ctx context.Context, task *commonv1.PollActivityTaskResponse) {
	w.mu.RLock()
	def, ok := w.activities[task.ActivityType]
	w.mu.RUnlock()
	if !ok {
		_, _ = w.svc.RespondActivityTaskFailed(ctx, &commonv1.RespondActivityTaskFailedRequest{
			TaskToken: task.TaskToken, Identity: w.identity,
			Failure:   &commonv1.Failure{Message: "no activity registered: " + task.ActivityType, Type: "NotRegistered"},
			Retryable: false,
		})
		return
	}

	actCtx := activity.WithInfo(ctx, activity.Info{
		WorkflowID:   task.WorkflowId,
		RunID:        task.RunId,
		ActivityID:   task.ActivityId,
		ActivityType: task.ActivityType,
		Attempt:      task.Attempt,
	})
	actCtx = activity.WithHeartbeat(actCtx, func(details []byte) error {
		_, err := w.svc.RecordActivityHeartbeat(ctx, &commonv1.RecordActivityHeartbeatRequest{
			TaskToken: task.TaskToken, Details: details,
		})
		return err
	})

	result, err := runActivity(actCtx, def, task.Input)
	if err != nil {
		_, respErr := w.svc.RespondActivityTaskFailed(ctx, &commonv1.RespondActivityTaskFailedRequest{
			TaskToken: task.TaskToken, Identity: w.identity,
			Failure:   &commonv1.Failure{Message: err.Error(), Type: "ActivityError"},
			Retryable: true,
		})
		if respErr != nil {
			w.logger.Warn("respond activity failed", "error", respErr)
		}
		return
	}

	if _, err := w.svc.RespondActivityTaskCompleted(ctx, &commonv1.RespondActivityTaskCompletedRequest{
		TaskToken: task.TaskToken, Identity: w.identity, Result: result,
	}); err != nil {
		w.logger.Warn("respond activity completed failed", "error", err)
	}
}

// runActivity invokes an activity, converting panics into errors so a buggy
// activity fails and retries rather than crashing the worker.
func runActivity(ctx context.Context, def activity.Definition, input []byte) (result []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("activity panic: %v", r)
		}
	}()
	return def(ctx, input)
}

// backoff pauses briefly after a poll error, unless the error is a benign
// context cancellation during shutdown.
func (w *Worker) backoff(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	if st, ok := status.FromError(err); ok && st.Code() == 1 { // Canceled
		return
	}
	if !errors.Is(err, context.Canceled) {
		w.logger.Debug("poll error, backing off", "error", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
	}
}
