// Package activity is the authoring surface for Chronos activities. Unlike
// workflows, activities may perform arbitrary side effects (network calls, DB
// writes); Chronos guarantees each activity result is recorded into history
// exactly once and, on replay, is never re-executed.
package activity

import "context"

// Definition is a registered activity implementation.
type Definition func(ctx context.Context, input []byte) ([]byte, error)

// Info carries metadata about the current activity attempt.
type Info struct {
	WorkflowID   string
	RunID        string
	ActivityID   string
	ActivityType string
	Attempt      int32
}

type ctxKey int

const (
	infoKey ctxKey = iota
	heartbeatKey
)

// HeartbeatFunc reports liveness for a long-running activity, extending its
// visibility lease so it is not redelivered while it makes progress.
type HeartbeatFunc func(details []byte) error

// WithInfo attaches activity metadata to the context (used by the worker).
func WithInfo(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, infoKey, info)
}

// WithHeartbeat attaches a heartbeat function to the context (used by the worker).
func WithHeartbeat(ctx context.Context, fn HeartbeatFunc) context.Context {
	return context.WithValue(ctx, heartbeatKey, fn)
}

// GetInfo returns the current activity's metadata.
func GetInfo(ctx context.Context) Info {
	if info, ok := ctx.Value(infoKey).(Info); ok {
		return info
	}
	return Info{}
}

// RecordHeartbeat reports activity liveness. It is a no-op when no heartbeat
// function is configured (e.g. in unit tests).
func RecordHeartbeat(ctx context.Context, details []byte) error {
	if fn, ok := ctx.Value(heartbeatKey).(HeartbeatFunc); ok && fn != nil {
		return fn(details)
	}
	return nil
}
