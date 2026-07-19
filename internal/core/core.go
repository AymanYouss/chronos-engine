// Package core holds the domain-level types shared by the storage layer and the
// control plane. It deliberately mirrors the generated protobuf types but keeps
// persistence concerns (leases, tokens, timers) out of the wire contract.
package core

import (
	"time"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TaskType distinguishes the two durable queues.
type TaskType int

const (
	// TaskTypeWorkflow is a task that advances workflow state via replay.
	TaskTypeWorkflow TaskType = iota
	// TaskTypeActivity is a task that runs a side-effecting activity.
	TaskTypeActivity
)

// WorkflowTask is a leased unit of workflow progress handed to a worker.
type WorkflowTask struct {
	TaskToken  int64
	WorkflowID string
	RunID      string
	Type       string
	History    []*commonv1.HistoryEvent
	// StartedEventID is the highest event id at poll time; it is the optimistic
	// concurrency token the worker echoes back when completing the task.
	StartedEventID int64
}

// ActivityTask is a leased unit of activity work handed to a worker.
type ActivityTask struct {
	TaskToken        int64
	WorkflowID       string
	RunID            string
	ActivityID       string
	ActivityType     string
	Input            []byte
	Attempt          int32
	ScheduledEventID int64
}

// DefaultActivityRetryPolicy is applied when a scheduled activity carries no
// explicit policy.
func DefaultActivityRetryPolicy() *commonv1.RetryPolicy {
	return &commonv1.RetryPolicy{
		InitialInterval:    durationpb.New(time.Second),
		BackoffCoefficient: 2.0,
		MaxInterval:        durationpb.New(100 * time.Second),
		MaxAttempts:        5,
	}
}

// NextRetryDelay computes the backoff delay for the given (1-based) attempt
// under an exponential policy, clamped to max_interval.
func NextRetryDelay(policy *commonv1.RetryPolicy, attempt int32) (time.Duration, bool) {
	if policy == nil {
		policy = DefaultActivityRetryPolicy()
	}
	if policy.MaxAttempts > 0 && attempt >= policy.MaxAttempts {
		return 0, false
	}
	initial := policy.InitialInterval.AsDuration()
	if initial <= 0 {
		initial = time.Second
	}
	coeff := policy.BackoffCoefficient
	if coeff < 1 {
		coeff = 2.0
	}
	delay := float64(initial)
	for i := int32(1); i < attempt; i++ {
		delay *= coeff
	}
	d := time.Duration(delay)
	if max := policy.MaxInterval.AsDuration(); max > 0 && d > max {
		d = max
	}
	return d, true
}
