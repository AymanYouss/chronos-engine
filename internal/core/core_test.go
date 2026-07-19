package core

import (
	"testing"
	"time"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestNextRetryDelayExponential(t *testing.T) {
	policy := &commonv1.RetryPolicy{
		InitialInterval:    durationpb.New(time.Second),
		BackoffCoefficient: 2.0,
		MaxInterval:        durationpb.New(time.Hour),
		MaxAttempts:        5,
	}
	cases := []struct {
		attempt  int32
		want     time.Duration
		canRetry bool
	}{
		{1, 1 * time.Second, true},
		{2, 2 * time.Second, true},
		{3, 4 * time.Second, true},
		{4, 8 * time.Second, true},
		{5, 0, false}, // attempts exhausted
	}
	for _, tc := range cases {
		got, ok := NextRetryDelay(policy, tc.attempt)
		if ok != tc.canRetry {
			t.Fatalf("attempt %d: canRetry = %v, want %v", tc.attempt, ok, tc.canRetry)
		}
		if ok && got != tc.want {
			t.Fatalf("attempt %d: delay = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestNextRetryDelayClampsToMax(t *testing.T) {
	policy := &commonv1.RetryPolicy{
		InitialInterval:    durationpb.New(time.Second),
		BackoffCoefficient: 10.0,
		MaxInterval:        durationpb.New(5 * time.Second),
		MaxAttempts:        0, // unlimited
	}
	got, ok := NextRetryDelay(policy, 4)
	if !ok {
		t.Fatal("expected retry allowed with unlimited attempts")
	}
	if got != 5*time.Second {
		t.Fatalf("delay = %v, want clamp to 5s", got)
	}
}

func TestNextRetryDelayNilPolicyUsesDefault(t *testing.T) {
	got, ok := NextRetryDelay(nil, 1)
	if !ok || got != time.Second {
		t.Fatalf("nil policy: got (%v, %v), want (1s, true)", got, ok)
	}
}
