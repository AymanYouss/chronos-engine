package workflow_test

import (
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/sdk/workflow"
)

// loopWorkflow schedules n sequential activities; used to build histories of a
// controlled size for replay benchmarking.
func loopWorkflow(n int) workflow.Definition {
	return func(ctx workflow.Context, _ []byte) ([]byte, error) {
		for i := 0; i < n; i++ {
			if err := ctx.ExecuteActivity("Step", i, workflow.ActivityOptions{}).Get(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
}

// buildLoopHistory constructs a fully-completed history of n activities so a
// replay pass walks all n events before producing the completion command.
func buildLoopHistory(n int) []*commonv1.HistoryEvent {
	events := []*commonv1.HistoryEvent{{
		EventType:  commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
		Attributes: &commonv1.HistoryEvent_WorkflowExecutionStarted{WorkflowExecutionStarted: &commonv1.WorkflowExecutionStartedAttributes{WorkflowType: "Loop", TaskQueue: "default"}},
	}}
	for i := 0; i < n; i++ {
		events = append(events, &commonv1.HistoryEvent{
			EventType:  commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &commonv1.HistoryEvent_ActivityTaskScheduled{ActivityTaskScheduled: &commonv1.ActivityTaskScheduledAttributes{ActivityType: "Step"}},
		})
		schedID := int64(len(events))
		events = append(events, &commonv1.HistoryEvent{
			EventType:  commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &commonv1.HistoryEvent_ActivityTaskCompleted{ActivityTaskCompleted: &commonv1.ActivityTaskCompletedAttributes{ScheduledEventId: schedID, Result: []byte("0")}},
		})
	}
	for i, e := range events {
		e.EventId = int64(i + 1)
		e.EventTime = timestamppb.New(time.Unix(int64(i), 0))
	}
	return events
}

func BenchmarkReplay(b *testing.B) {
	for _, n := range []int{10, 50, 100, 500, 1000} {
		def := loopWorkflow(n)
		history := buildLoopHistory(n)
		b.Run(fmt.Sprintf("events=%d", len(history)), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := workflow.Execute(def, "default", history, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
