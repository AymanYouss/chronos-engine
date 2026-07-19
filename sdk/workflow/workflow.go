// Package workflow is the authoring surface for Chronos workflows. Workflow
// code must be deterministic: it may only interact with the outside world
// through this Context, which records every decision as a command and replays
// results from history. Given the same history, a workflow always produces the
// same sequence of commands, which is what makes crash-resume exact.
package workflow

import (
	"errors"
	"log/slog"
	"time"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
)

// Definition is a registered workflow implementation. Input and output are
// opaque payloads; use the converter package to (de)serialize typed values.
type Definition func(ctx Context, input []byte) ([]byte, error)

// Context is the deterministic execution context handed to workflow code.
type Context interface {
	// ExecuteActivity schedules an activity and returns a Future for its result.
	// The call itself does not block; block by calling Future.Get.
	ExecuteActivity(activityType string, input any, opts ActivityOptions) Future
	// NewTimer starts a durable timer and returns a Future that resolves when
	// the timer fires.
	NewTimer(d time.Duration) Future
	// Sleep durably blocks the workflow for the given duration.
	Sleep(d time.Duration) error
	// Now returns the deterministic workflow clock (advances only via events).
	Now() time.Time
	// ReceiveSignal returns the next unconsumed signal with the given name.
	ReceiveSignal(name string) (input []byte, ok bool)
	// AwaitSignal blocks until a signal with the given name is available and
	// returns its payload.
	AwaitSignal(name string) []byte
	// Logger returns a replay-aware logger that suppresses output during replay
	// so a resumed workflow does not re-log historical steps.
	Logger() *slog.Logger
}

// Future is a handle to an asynchronous result inside a workflow.
type Future interface {
	// Get blocks the workflow until the result is available and decodes it into
	// out (which may be nil to ignore the value). It returns the activity error
	// if the operation failed terminally.
	Get(out any) error
	// IsReady reports whether the result is already available without blocking.
	IsReady() bool
}

// ActivityOptions configures a scheduled activity.
type ActivityOptions struct {
	// TaskQueue routes the activity; defaults to the workflow's task queue.
	TaskQueue string
	// StartToCloseTimeout bounds a single activity attempt.
	StartToCloseTimeout time.Duration
	// RetryPolicy overrides the default exponential backoff policy.
	RetryPolicy *RetryPolicy
	// ActivityID overrides the deterministic auto-generated id.
	ActivityID string
}

// RetryPolicy configures activity retries.
type RetryPolicy struct {
	InitialInterval    time.Duration
	BackoffCoefficient float64
	MaxInterval        time.Duration
	MaxAttempts        int32
}

// ToProto converts the retry policy to its wire representation.
func (p *RetryPolicy) ToProto() *commonv1.RetryPolicy {
	if p == nil {
		return nil
	}
	return &commonv1.RetryPolicy{
		InitialInterval:    durationProto(p.InitialInterval),
		BackoffCoefficient: p.BackoffCoefficient,
		MaxInterval:        durationProto(p.MaxInterval),
		MaxAttempts:        p.MaxAttempts,
	}
}

// ActivityError wraps a terminal activity failure surfaced to the workflow.
type ActivityError struct {
	ActivityType string
	Message      string
}

func (e *ActivityError) Error() string {
	return e.ActivityType + ": " + e.Message
}

// ErrNonDeterministic indicates workflow code diverged from its recorded
// history, which almost always means the code changed incompatibly.
var ErrNonDeterministic = errors.New("nondeterministic workflow execution")
