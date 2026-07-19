// Package server implements the Chronos control-plane gRPC service on top of
// the storage layer. It is intentionally thin: all durability invariants live
// in storage; this layer handles protocol concerns, long-polling, and metrics.
package server

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/internal/metrics"
	"github.com/AymanYouss/chronos-engine/internal/storage"
)

// Service implements commonv1.WorkflowServiceServer.
type Service struct {
	commonv1.UnimplementedWorkflowServiceServer

	store    storage.Store
	metrics  *metrics.Metrics
	lease    time.Duration
	pollWait time.Duration
}

// Options configures the service.
type Options struct {
	Lease    time.Duration
	PollWait time.Duration
}

// New constructs a control-plane service.
func New(store storage.Store, m *metrics.Metrics, opts Options) *Service {
	if opts.Lease <= 0 {
		opts.Lease = 30 * time.Second
	}
	if opts.PollWait <= 0 {
		opts.PollWait = 5 * time.Second
	}
	return &Service{store: store, metrics: m, lease: opts.Lease, pollWait: opts.PollWait}
}

// StartWorkflow enqueues a new execution, idempotent on workflow_id.
func (s *Service) StartWorkflow(ctx context.Context, req *commonv1.StartWorkflowRequest) (*commonv1.StartWorkflowResponse, error) {
	if req.WorkflowId == "" || req.WorkflowType == "" || req.TaskQueue == "" {
		return nil, status.Error(codes.InvalidArgument, "workflow_id, workflow_type and task_queue are required")
	}
	runID, already, err := s.store.StartWorkflow(ctx, storage.StartWorkflowParams{
		WorkflowID:       req.WorkflowId,
		WorkflowType:     req.WorkflowType,
		TaskQueue:        req.TaskQueue,
		Input:            req.Input,
		RetryPolicy:      req.RetryPolicy,
		ExecutionTimeout: req.WorkflowExecutionTimeout.AsDuration(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	if !already {
		s.metrics.WorkflowsStarted.Inc()
	}
	return &commonv1.StartWorkflowResponse{
		WorkflowId:     req.WorkflowId,
		RunId:          runID,
		AlreadyStarted: already,
	}, nil
}

// SignalWorkflow delivers an external signal.
func (s *Service) SignalWorkflow(ctx context.Context, req *commonv1.SignalWorkflowRequest) (*commonv1.SignalWorkflowResponse, error) {
	if err := s.store.SignalWorkflow(ctx, req.WorkflowId, req.RunId, req.SignalName, req.Input); err != nil {
		return nil, toStatus(err)
	}
	return &commonv1.SignalWorkflowResponse{}, nil
}

// DescribeWorkflow returns execution metadata.
func (s *Service) DescribeWorkflow(ctx context.Context, req *commonv1.DescribeWorkflowRequest) (*commonv1.DescribeWorkflowResponse, error) {
	exec, err := s.store.DescribeWorkflow(ctx, req.WorkflowId, req.RunId)
	if err != nil {
		return nil, toStatus(err)
	}
	return &commonv1.DescribeWorkflowResponse{Execution: toProtoExecution(exec)}, nil
}

// GetWorkflowHistory returns the full event history.
func (s *Service) GetWorkflowHistory(ctx context.Context, req *commonv1.GetWorkflowHistoryRequest) (*commonv1.GetWorkflowHistoryResponse, error) {
	history, err := s.store.GetHistory(ctx, req.WorkflowId, req.RunId)
	if err != nil {
		return nil, toStatus(err)
	}
	return &commonv1.GetWorkflowHistoryResponse{Events: history}, nil
}

// ListWorkflows returns executions filtered by status.
func (s *Service) ListWorkflows(ctx context.Context, req *commonv1.ListWorkflowsRequest) (*commonv1.ListWorkflowsResponse, error) {
	limit := int(req.PageSize)
	offset := decodeOffset(req.PageToken)
	execs, err := s.store.ListWorkflows(ctx, req.StatusFilter, limit, offset)
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*commonv1.WorkflowExecution, 0, len(execs))
	for _, e := range execs {
		out = append(out, toProtoExecution(e))
	}
	var next string
	if len(execs) > 0 && (limit <= 0 || len(execs) == limit) {
		next = encodeOffset(offset + len(execs))
	}
	return &commonv1.ListWorkflowsResponse{Executions: out, NextPageToken: next}, nil
}

// PollWorkflowTask long-polls for the next workflow task.
func (s *Service) PollWorkflowTask(ctx context.Context, req *commonv1.PollWorkflowTaskRequest) (*commonv1.PollWorkflowTaskResponse, error) {
	deadline := time.Now().Add(s.pollWait)
	for {
		task, err := s.store.PollWorkflowTask(ctx, req.TaskQueue, req.Identity, s.lease)
		if err != nil {
			return nil, toStatus(err)
		}
		if task != nil {
			s.metrics.WorkflowTasksPolled.Inc()
			return &commonv1.PollWorkflowTaskResponse{
				WorkflowId:     task.WorkflowID,
				RunId:          task.RunID,
				WorkflowType:   task.Type,
				TaskToken:      task.TaskToken,
				History:        task.History,
				StartedEventId: task.StartedEventID,
			}, nil
		}
		if !s.sleepPoll(ctx, deadline) {
			return &commonv1.PollWorkflowTaskResponse{}, nil
		}
	}
}

// RespondWorkflowTaskCompleted commits a workflow decision's commands.
func (s *Service) RespondWorkflowTaskCompleted(ctx context.Context, req *commonv1.RespondWorkflowTaskCompletedRequest) (*commonv1.RespondWorkflowTaskCompletedResponse, error) {
	err := s.store.CompleteWorkflowTask(ctx, req.TaskToken, req.Identity, req.Commands)
	if errors.Is(err, storage.ErrTaskLost) || errors.Is(err, storage.ErrConflict) {
		// Benign: a duplicate delivery lost the race; state is already correct.
		return &commonv1.RespondWorkflowTaskCompletedResponse{}, nil
	}
	if err != nil {
		return nil, toStatus(err)
	}
	for _, cmd := range req.Commands {
		switch cmd.Attributes.(type) {
		case *commonv1.Command_CompleteWorkflowExecution:
			s.metrics.WorkflowsCompleted.Inc()
		case *commonv1.Command_FailWorkflowExecution:
			s.metrics.WorkflowsFailed.Inc()
		}
	}
	return &commonv1.RespondWorkflowTaskCompletedResponse{}, nil
}

// PollActivityTask long-polls for the next activity task.
func (s *Service) PollActivityTask(ctx context.Context, req *commonv1.PollActivityTaskRequest) (*commonv1.PollActivityTaskResponse, error) {
	deadline := time.Now().Add(s.pollWait)
	for {
		task, err := s.store.PollActivityTask(ctx, req.TaskQueue, req.Identity, s.lease)
		if err != nil {
			return nil, toStatus(err)
		}
		if task != nil {
			s.metrics.ActivityTasksPolled.Inc()
			s.metrics.ActivitiesStarted.Inc()
			return &commonv1.PollActivityTaskResponse{
				WorkflowId:       task.WorkflowID,
				RunId:            task.RunID,
				ActivityId:       task.ActivityID,
				ActivityType:     task.ActivityType,
				TaskToken:        task.TaskToken,
				Input:            task.Input,
				Attempt:          task.Attempt,
				ScheduledEventId: task.ScheduledEventID,
			}, nil
		}
		if !s.sleepPoll(ctx, deadline) {
			return &commonv1.PollActivityTaskResponse{}, nil
		}
	}
}

// RespondActivityTaskCompleted records an activity result exactly once.
func (s *Service) RespondActivityTaskCompleted(ctx context.Context, req *commonv1.RespondActivityTaskCompletedRequest) (*commonv1.RespondActivityTaskCompletedResponse, error) {
	err := s.store.CompleteActivityTask(ctx, req.TaskToken, req.Identity, req.Result)
	if errors.Is(err, storage.ErrTaskLost) {
		return &commonv1.RespondActivityTaskCompletedResponse{}, nil
	}
	if err != nil {
		return nil, toStatus(err)
	}
	s.metrics.ActivitiesComplete.Inc()
	return &commonv1.RespondActivityTaskCompletedResponse{}, nil
}

// RespondActivityTaskFailed records a failure and applies the retry policy.
func (s *Service) RespondActivityTaskFailed(ctx context.Context, req *commonv1.RespondActivityTaskFailedRequest) (*commonv1.RespondActivityTaskFailedResponse, error) {
	retried, err := s.store.FailActivityTask(ctx, req.TaskToken, req.Identity, req.Failure, req.Retryable)
	if errors.Is(err, storage.ErrTaskLost) {
		return &commonv1.RespondActivityTaskFailedResponse{}, nil
	}
	if err != nil {
		return nil, toStatus(err)
	}
	if retried {
		s.metrics.ActivitiesRetried.Inc()
	} else {
		s.metrics.ActivitiesFailed.Inc()
	}
	return &commonv1.RespondActivityTaskFailedResponse{}, nil
}

// RecordActivityHeartbeat extends the activity lease.
func (s *Service) RecordActivityHeartbeat(ctx context.Context, req *commonv1.RecordActivityHeartbeatRequest) (*commonv1.RecordActivityHeartbeatResponse, error) {
	if _, err := s.store.ExtendActivityLease(ctx, req.TaskToken, s.lease); err != nil {
		return nil, toStatus(err)
	}
	return &commonv1.RecordActivityHeartbeatResponse{}, nil
}

// sleepPoll waits a short interval between empty polls, honoring the deadline
// and context cancellation. It returns false when polling should stop.
func (s *Service) sleepPoll(ctx context.Context, deadline time.Time) bool {
	if time.Now().After(deadline) {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(200 * time.Millisecond):
		return true
	}
}

func toProtoExecution(e *storage.Execution) *commonv1.WorkflowExecution {
	pe := &commonv1.WorkflowExecution{
		WorkflowId:    e.WorkflowID,
		RunId:         e.RunID,
		WorkflowType:  e.WorkflowType,
		TaskQueue:     e.TaskQueue,
		Status:        e.Status,
		StartTime:     timestamppb.New(e.StartTime),
		HistoryLength: e.HistoryLength,
	}
	if e.CloseTime != nil {
		pe.CloseTime = timestamppb.New(*e.CloseTime)
	}
	return pe
}

func toStatus(err error) error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, storage.ErrConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, storage.ErrTaskLost):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
