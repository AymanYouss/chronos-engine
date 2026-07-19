// Package storage defines the persistence contract for the Chronos control
// plane and its Postgres implementation. Every durability guarantee in the
// engine (exactly-once activity recording, deterministic resume, durable
// timers) is enforced here inside serializable-safe SQL transactions.
package storage

import (
	"context"
	"errors"
	"time"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/internal/core"
)

// ErrNotFound is returned when an execution or task does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on an optimistic-concurrency failure, i.e. history
// advanced underneath a stale worker. Callers treat it as a benign no-op.
var ErrConflict = errors.New("conflict: history advanced")

// ErrTaskLost is returned when a task token no longer maps to a live lease,
// which happens when a duplicate delivery is completed after the original.
var ErrTaskLost = errors.New("task no longer available")

// StartWorkflowParams describes a new execution request.
type StartWorkflowParams struct {
	WorkflowID       string
	WorkflowType     string
	TaskQueue        string
	Input            []byte
	RetryPolicy      *commonv1.RetryPolicy
	ExecutionTimeout time.Duration
}

// Execution is the projected state of a workflow run.
type Execution struct {
	WorkflowID    string
	RunID         string
	WorkflowType  string
	TaskQueue     string
	Status        commonv1.WorkflowStatus
	StartTime     time.Time
	CloseTime     *time.Time
	HistoryLength int64
}

// Store is the full persistence surface used by the control plane.
type Store interface {
	// Migrate applies all embedded schema migrations.
	Migrate(ctx context.Context) error
	// Ping verifies connectivity.
	Ping(ctx context.Context) error
	// Close releases the connection pool.
	Close()

	// --- Client operations ---

	StartWorkflow(ctx context.Context, p StartWorkflowParams) (runID string, alreadyStarted bool, err error)
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, input []byte) error
	DescribeWorkflow(ctx context.Context, workflowID, runID string) (*Execution, error)
	GetHistory(ctx context.Context, workflowID, runID string) ([]*commonv1.HistoryEvent, error)
	ListWorkflows(ctx context.Context, statusFilter commonv1.WorkflowStatus, limit, offset int) ([]*Execution, error)
	CountByStatus(ctx context.Context) (map[commonv1.WorkflowStatus]int64, error)

	// --- Worker operations ---

	PollWorkflowTask(ctx context.Context, taskQueue, identity string, lease time.Duration) (*core.WorkflowTask, error)
	CompleteWorkflowTask(ctx context.Context, token int64, identity string, commands []*commonv1.Command) error
	PollActivityTask(ctx context.Context, taskQueue, identity string, lease time.Duration) (*core.ActivityTask, error)
	CompleteActivityTask(ctx context.Context, token int64, identity string, result []byte) error
	// FailActivityTask records an activity failure and reports whether the
	// activity was rescheduled for retry (true) or failed terminally (false).
	FailActivityTask(ctx context.Context, token int64, identity string, failure *commonv1.Failure, retryable bool) (retried bool, err error)
	ExtendActivityLease(ctx context.Context, token int64, lease time.Duration) (bool, error)

	// --- Background services ---

	// FireDueTimers advances all timers whose deadline has passed, appending
	// TIMER_FIRED and scheduling workflow tasks. Returns the number fired.
	FireDueTimers(ctx context.Context, now time.Time, batch int) (int, error)
}
