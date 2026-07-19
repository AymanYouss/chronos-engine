package workflow_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/sdk/converter"
	"github.com/AymanYouss/chronos-engine/sdk/workflow"
)

// orderWorkflow: charge -> wait (timer) -> ship -> return confirmation.
// This mirrors the sample order-fulfillment workflow and exercises activities
// and a durable timer in sequence.
func orderWorkflow(ctx workflow.Context, input []byte) ([]byte, error) {
	var charge string
	if err := ctx.ExecuteActivity("ChargePayment", "order-1", workflow.ActivityOptions{}).Get(&charge); err != nil {
		return nil, err
	}
	if err := ctx.Sleep(time.Minute); err != nil {
		return nil, err
	}
	var ship string
	if err := ctx.ExecuteActivity("ShipOrder", charge, workflow.ActivityOptions{}).Get(&ship); err != nil {
		return nil, err
	}
	out, _ := converter.Encode(map[string]string{"charge": charge, "ship": ship})
	return out, nil
}

func countCommands(cmds []*commonv1.Command) (schedule, timer, complete, fail int) {
	for _, c := range cmds {
		switch c.Attributes.(type) {
		case *commonv1.Command_ScheduleActivityTask:
			schedule++
		case *commonv1.Command_StartTimer:
			timer++
		case *commonv1.Command_CompleteWorkflowExecution:
			complete++
		case *commonv1.Command_FailWorkflowExecution:
			fail++
		}
	}
	return
}

func TestReplaySchedulesFirstActivity(t *testing.T) {
	history := build(started())
	res, err := workflow.Execute(orderWorkflow, "default", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	sched, timer, complete, _ := countCommands(res.Commands)
	if sched != 1 || timer != 0 || complete != 0 {
		t.Fatalf("first decision: got schedule=%d timer=%d complete=%d, want 1/0/0", sched, timer, complete)
	}
	if res.Completed {
		t.Fatal("workflow should not be complete after first decision")
	}
	if got := res.Commands[0].GetScheduleActivityTask().ActivityType; got != "ChargePayment" {
		t.Fatalf("scheduled %q, want ChargePayment", got)
	}
}

// The critical durability property: after ChargePayment completed and the
// worker crashed, resuming replays history and must NOT reschedule
// ChargePayment. It should advance to the timer instead.
func TestResumeDoesNotReExecuteCompletedActivity(t *testing.T) {
	history := build(
		started(),
		actScheduled("ChargePayment"), // event 2
		actCompleted(2, `"charged-42"`),
	)
	res, err := workflow.Execute(orderWorkflow, "default", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	sched, timer, _, _ := countCommands(res.Commands)
	if sched != 0 {
		t.Fatalf("resume rescheduled %d activities; a completed activity must never re-run", sched)
	}
	if timer != 1 {
		t.Fatalf("resume should advance to the timer, got timer=%d", timer)
	}
}

func TestResumeAfterTimerFiredSchedulesNextActivity(t *testing.T) {
	history := build(
		started(),
		actScheduled("ChargePayment"), // 2
		actCompleted(2, `"charged-42"`),
		timerStarted(), // 4
		timerFired(4),
	)
	res, err := workflow.Execute(orderWorkflow, "default", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	sched, timer, _, _ := countCommands(res.Commands)
	if sched != 1 || timer != 0 {
		t.Fatalf("after timer: got schedule=%d timer=%d, want 1/0", sched, timer)
	}
	if got := res.Commands[0].GetScheduleActivityTask().ActivityType; got != "ShipOrder" {
		t.Fatalf("scheduled %q, want ShipOrder", got)
	}
}

func TestWorkflowCompletesWhenAllStepsDone(t *testing.T) {
	history := build(
		started(),
		actScheduled("ChargePayment"), // 2
		actCompleted(2, `"charged-42"`),
		timerStarted(), // 4
		timerFired(4),
		actScheduled("ShipOrder"), // 6
		actCompleted(6, `"shipped-42"`),
	)
	res, err := workflow.Execute(orderWorkflow, "default", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, complete, fail := countCommands(res.Commands)
	if complete != 1 || fail != 0 {
		t.Fatalf("final decision: got complete=%d fail=%d, want 1/0", complete, fail)
	}
	if !res.Completed {
		t.Fatal("workflow should be complete")
	}
}

// Determinism: replaying identical history must yield identical commands.
func TestReplayIsDeterministic(t *testing.T) {
	history := build(started())
	first, _ := workflow.Execute(orderWorkflow, "default", history, nil)
	second, _ := workflow.Execute(orderWorkflow, "default", history, nil)
	if len(first.Commands) != len(second.Commands) {
		t.Fatalf("nondeterministic command count: %d vs %d", len(first.Commands), len(second.Commands))
	}
	a := first.Commands[0].GetScheduleActivityTask()
	b := second.Commands[0].GetScheduleActivityTask()
	if a.ActivityType != b.ActivityType || a.ActivityId != b.ActivityId {
		t.Fatalf("nondeterministic command: %v vs %v", a, b)
	}
}

// A failed activity surfaces to the workflow as an error, failing the workflow.
func TestTerminalActivityFailureFailsWorkflow(t *testing.T) {
	history := build(
		started(),
		actScheduled("ChargePayment"), // 2
		actFailed(2, "card declined"),
	)
	res, err := workflow.Execute(orderWorkflow, "default", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, fail := countCommands(res.Commands)
	if fail != 1 {
		t.Fatalf("expected 1 fail-workflow command, got %d", fail)
	}
}

// Changing scheduled activity order relative to history is detected as
// nondeterminism and fails the workflow rather than corrupting state.
func TestNonDeterminismIsDetected(t *testing.T) {
	history := build(
		started(),
		actScheduled("ShipOrder"), // history says first activity was ShipOrder
	)
	res, err := workflow.Execute(orderWorkflow, "default", history, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, fail := countCommands(res.Commands)
	if fail != 1 {
		t.Fatalf("expected nondeterminism to fail the workflow, got fail=%d", fail)
	}
}

// --- history builders ---

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func build(events ...*commonv1.HistoryEvent) []*commonv1.HistoryEvent {
	for i, e := range events {
		e.EventId = int64(i + 1)
		e.EventTime = timestamppb.New(base.Add(time.Duration(i) * time.Second))
	}
	return events
}

func started() *commonv1.HistoryEvent {
	return &commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
		Attributes: &commonv1.HistoryEvent_WorkflowExecutionStarted{
			WorkflowExecutionStarted: &commonv1.WorkflowExecutionStartedAttributes{
				WorkflowType: "OrderWorkflow", TaskQueue: "default",
			},
		},
	}
}

func actScheduled(actType string) *commonv1.HistoryEvent {
	return &commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
		Attributes: &commonv1.HistoryEvent_ActivityTaskScheduled{
			ActivityTaskScheduled: &commonv1.ActivityTaskScheduledAttributes{ActivityType: actType},
		},
	}
}

func actCompleted(scheduledID int64, resultJSON string) *commonv1.HistoryEvent {
	return &commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
		Attributes: &commonv1.HistoryEvent_ActivityTaskCompleted{
			ActivityTaskCompleted: &commonv1.ActivityTaskCompletedAttributes{
				ScheduledEventId: scheduledID, Result: []byte(resultJSON),
			},
		},
	}
}

func actFailed(scheduledID int64, msg string) *commonv1.HistoryEvent {
	return &commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_FAILED,
		Attributes: &commonv1.HistoryEvent_ActivityTaskFailed{
			ActivityTaskFailed: &commonv1.ActivityTaskFailedAttributes{
				ScheduledEventId: scheduledID, Failure: &commonv1.Failure{Message: msg},
			},
		},
	}
}

func timerStarted() *commonv1.HistoryEvent {
	return &commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_TIMER_STARTED,
		Attributes: &commonv1.HistoryEvent_TimerStarted{
			TimerStarted: &commonv1.TimerStartedAttributes{TimerId: "timer-0"},
		},
	}
}

func timerFired(startedID int64) *commonv1.HistoryEvent {
	return &commonv1.HistoryEvent{
		EventType: commonv1.EventType_EVENT_TYPE_TIMER_FIRED,
		Attributes: &commonv1.HistoryEvent_TimerFired{
			TimerFired: &commonv1.TimerFiredAttributes{StartedEventId: startedID, TimerId: "timer-0"},
		},
	}
}
