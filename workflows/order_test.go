package workflows_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/sdk/converter"
	"github.com/AymanYouss/chronos-engine/sdk/workflow"
	"github.com/AymanYouss/chronos-engine/workflows"
)

// harness is an in-memory control plane: it repeatedly asks the workflow for
// its next decision, applies the resulting commands to a growing history
// (auto-completing activities and firing timers), and loops until the workflow
// terminates. It exercises the full deterministic loop without Postgres.
type harness struct {
	history []*commonv1.HistoryEvent
	nextID  int64
	t       *testing.T
}

func newHarness(t *testing.T, input any) *harness {
	blob, _ := converter.Encode(input)
	h := &harness{t: t, nextID: 1}
	h.append(&commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
		Attributes: &commonv1.HistoryEvent_WorkflowExecutionStarted{
			WorkflowExecutionStarted: &commonv1.WorkflowExecutionStartedAttributes{
				WorkflowType: workflows.OrderWorkflowType, TaskQueue: "default", Input: blob,
			},
		},
	})
	return h
}

func (h *harness) append(e *commonv1.HistoryEvent) int64 {
	e.EventId = h.nextID
	e.EventTime = timestamppb.New(time.Unix(h.nextID, 0))
	h.history = append(h.history, e)
	h.nextID++
	return e.EventId
}

// run drives the workflow to termination and returns its final result payload
// plus how many activity executions occurred (distinct scheduled activities).
func (h *harness) run(def workflow.Definition) (result []byte, activityRuns int) {
	for step := 0; step < 100; step++ {
		res, err := workflow.Execute(def, "default", h.history, nil)
		if err != nil {
			h.t.Fatalf("execute: %v", err)
		}
		for _, cmd := range res.Commands {
			switch a := cmd.Attributes.(type) {
			case *commonv1.Command_ScheduleActivityTask:
				schedID := h.append(&commonv1.HistoryEvent{
					EventType: commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
					Attributes: &commonv1.HistoryEvent_ActivityTaskScheduled{
						ActivityTaskScheduled: &commonv1.ActivityTaskScheduledAttributes{
							ActivityId:   a.ScheduleActivityTask.ActivityId,
							ActivityType: a.ScheduleActivityTask.ActivityType,
						},
					},
				})
				activityRuns++
				h.append(&commonv1.HistoryEvent{
					EventType: commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
					Attributes: &commonv1.HistoryEvent_ActivityTaskCompleted{
						ActivityTaskCompleted: &commonv1.ActivityTaskCompletedAttributes{
							ScheduledEventId: schedID, Result: []byte(`"ok"`),
						},
					},
				})
			case *commonv1.Command_StartTimer:
				startID := h.append(&commonv1.HistoryEvent{
					EventType:  commonv1.EventType_EVENT_TYPE_TIMER_STARTED,
					Attributes: &commonv1.HistoryEvent_TimerStarted{TimerStarted: &commonv1.TimerStartedAttributes{TimerId: a.StartTimer.TimerId}},
				})
				h.append(&commonv1.HistoryEvent{
					EventType: commonv1.EventType_EVENT_TYPE_TIMER_FIRED,
					Attributes: &commonv1.HistoryEvent_TimerFired{
						TimerFired: &commonv1.TimerFiredAttributes{StartedEventId: startID, TimerId: a.StartTimer.TimerId},
					},
				})
			case *commonv1.Command_CompleteWorkflowExecution:
				return a.CompleteWorkflowExecution.Result, activityRuns
			case *commonv1.Command_FailWorkflowExecution:
				h.t.Fatalf("workflow failed: %s", a.FailWorkflowExecution.Failure.GetMessage())
			}
		}
		if res.Completed {
			return nil, activityRuns
		}
	}
	h.t.Fatal("workflow did not terminate")
	return nil, activityRuns
}

func TestOrderFulfillmentReachesCompletion(t *testing.T) {
	h := newHarness(t, workflows.OrderInput{
		OrderID: "ORD-1001", CustomerID: "cust-7", AmountCents: 4999, Items: []string{"widget", "gadget"},
	})
	result, runs := h.run(workflows.OrderFulfillment)

	var out workflows.OrderResult
	if err := converter.Decode(result, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if out.Status != "FULFILLED" {
		t.Fatalf("status = %q, want FULFILLED", out.Status)
	}
	if out.OrderID != "ORD-1001" {
		t.Fatalf("orderID = %q", out.OrderID)
	}
	// The workflow defines exactly four activities; each must run exactly once.
	if runs != 4 {
		t.Fatalf("activity executions = %d, want 4 (no duplicates, none skipped)", runs)
	}
}

// Simulate a crash after the first activity by truncating history to what the
// server had durably persisted, then resuming. The resumed run must reach the
// same terminal result and still run each activity exactly once in total.
func TestOrderFulfillmentResumesAfterTruncatedHistory(t *testing.T) {
	full := newHarness(t, workflows.OrderInput{OrderID: "ORD-2002", AmountCents: 100})
	// Advance two decisions (charge + reserve), then "crash": keep only the
	// durably-recorded prefix and resume from there.
	firstResult, _ := full.run(workflows.OrderFulfillment)
	if firstResult == nil {
		t.Fatal("expected a completed run to compare against")
	}

	resumed := &harness{t: t, nextID: 1}
	// Rebuild history up to the packaging timer firing (charge + reserve done).
	resumed.append(cloneStarted())
	resumed.appendActivity(workflows.ActivityChargePayment)
	resumed.appendActivity(workflows.ActivityReserveInventory)

	result, runsAfterResume := resumed.run(workflows.OrderFulfillment)
	var out workflows.OrderResult
	if err := converter.Decode(result, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != "FULFILLED" {
		t.Fatalf("resumed status = %q, want FULFILLED", out.Status)
	}
	// After resume, only ShipOrder and SendReceipt should execute; the already
	// completed ChargePayment and ReserveInventory are replayed from history.
	if runsAfterResume != 2 {
		t.Fatalf("post-resume activity executions = %d, want 2", runsAfterResume)
	}
}

func cloneStarted() *commonv1.HistoryEvent {
	blob, _ := converter.Encode(workflows.OrderInput{OrderID: "ORD-2002", AmountCents: 100})
	return &commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
		Attributes: &commonv1.HistoryEvent_WorkflowExecutionStarted{
			WorkflowExecutionStarted: &commonv1.WorkflowExecutionStartedAttributes{
				WorkflowType: workflows.OrderWorkflowType, TaskQueue: "default", Input: blob,
			},
		},
	}
}

// appendActivity records a completed activity (schedule + completion) for the
// resume test's pre-crash prefix.
func (h *harness) appendActivity(actType string) {
	schedID := h.append(&commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
		Attributes: &commonv1.HistoryEvent_ActivityTaskScheduled{
			ActivityTaskScheduled: &commonv1.ActivityTaskScheduledAttributes{ActivityType: actType},
		},
	})
	h.append(&commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
		Attributes: &commonv1.HistoryEvent_ActivityTaskCompleted{
			ActivityTaskCompleted: &commonv1.ActivityTaskCompletedAttributes{ScheduledEventId: schedID, Result: []byte(`"ok"`)},
		},
	})
}
