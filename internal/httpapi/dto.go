package httpapi

import (
	"encoding/base64"
	"time"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/internal/storage"
)

type executionDTO struct {
	WorkflowID    string     `json:"workflowId"`
	RunID         string     `json:"runId"`
	WorkflowType  string     `json:"workflowType"`
	TaskQueue     string     `json:"taskQueue"`
	Status        string     `json:"status"`
	StartTime     time.Time  `json:"startTime"`
	CloseTime     *time.Time `json:"closeTime,omitempty"`
	HistoryLength int64      `json:"historyLength"`
}

type eventDTO struct {
	EventID int64          `json:"eventId"`
	Type    string         `json:"type"`
	Time    time.Time      `json:"time"`
	Details map[string]any `json:"details,omitempty"`
}

func toExecutionDTO(e *storage.Execution) executionDTO {
	return executionDTO{
		WorkflowID:    e.WorkflowID,
		RunID:         e.RunID,
		WorkflowType:  e.WorkflowType,
		TaskQueue:     e.TaskQueue,
		Status:        statusName(e.Status),
		StartTime:     e.StartTime,
		CloseTime:     e.CloseTime,
		HistoryLength: e.HistoryLength,
	}
}

func toEventDTO(e *commonv1.HistoryEvent) eventDTO {
	dto := eventDTO{
		EventID: e.EventId,
		Type:    eventTypeName(e.EventType),
		Details: map[string]any{},
	}
	if e.EventTime != nil {
		dto.Time = e.EventTime.AsTime()
	}
	switch a := e.Attributes.(type) {
	case *commonv1.HistoryEvent_WorkflowExecutionStarted:
		dto.Details["workflowType"] = a.WorkflowExecutionStarted.WorkflowType
		dto.Details["taskQueue"] = a.WorkflowExecutionStarted.TaskQueue
		dto.Details["input"] = preview(a.WorkflowExecutionStarted.Input)
	case *commonv1.HistoryEvent_ActivityTaskScheduled:
		dto.Details["activityId"] = a.ActivityTaskScheduled.ActivityId
		dto.Details["activityType"] = a.ActivityTaskScheduled.ActivityType
		dto.Details["input"] = preview(a.ActivityTaskScheduled.Input)
	case *commonv1.HistoryEvent_ActivityTaskStarted:
		dto.Details["scheduledEventId"] = a.ActivityTaskStarted.ScheduledEventId
		dto.Details["worker"] = a.ActivityTaskStarted.WorkerIdentity
		dto.Details["attempt"] = a.ActivityTaskStarted.Attempt
	case *commonv1.HistoryEvent_ActivityTaskCompleted:
		dto.Details["scheduledEventId"] = a.ActivityTaskCompleted.ScheduledEventId
		dto.Details["result"] = preview(a.ActivityTaskCompleted.Result)
	case *commonv1.HistoryEvent_ActivityTaskFailed:
		dto.Details["scheduledEventId"] = a.ActivityTaskFailed.ScheduledEventId
		dto.Details["retryable"] = a.ActivityTaskFailed.Retryable
		if a.ActivityTaskFailed.Failure != nil {
			dto.Details["error"] = a.ActivityTaskFailed.Failure.Message
		}
	case *commonv1.HistoryEvent_ActivityTaskRetryScheduled:
		dto.Details["scheduledEventId"] = a.ActivityTaskRetryScheduled.ScheduledEventId
		dto.Details["attempt"] = a.ActivityTaskRetryScheduled.Attempt
		if a.ActivityTaskRetryScheduled.NextAttemptTime != nil {
			dto.Details["nextAttempt"] = a.ActivityTaskRetryScheduled.NextAttemptTime.AsTime()
		}
		if a.ActivityTaskRetryScheduled.Failure != nil {
			dto.Details["error"] = a.ActivityTaskRetryScheduled.Failure.Message
		}
	case *commonv1.HistoryEvent_TimerStarted:
		dto.Details["timerId"] = a.TimerStarted.TimerId
		if a.TimerStarted.FireTime != nil {
			dto.Details["fireTime"] = a.TimerStarted.FireTime.AsTime()
		}
	case *commonv1.HistoryEvent_TimerFired:
		dto.Details["timerId"] = a.TimerFired.TimerId
		dto.Details["startedEventId"] = a.TimerFired.StartedEventId
	case *commonv1.HistoryEvent_WorkflowExecutionSignaled:
		dto.Details["signalName"] = a.WorkflowExecutionSignaled.SignalName
		dto.Details["input"] = preview(a.WorkflowExecutionSignaled.Input)
	case *commonv1.HistoryEvent_WorkflowExecutionCompleted:
		dto.Details["result"] = preview(a.WorkflowExecutionCompleted.Result)
	case *commonv1.HistoryEvent_WorkflowExecutionFailed:
		if a.WorkflowExecutionFailed.Failure != nil {
			dto.Details["error"] = a.WorkflowExecutionFailed.Failure.Message
		}
	}
	return dto
}

// preview renders small payloads as UTF-8 text and larger/binary ones as a
// base64 snippet so the inspector always has something readable to show.
func preview(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if isPrintable(b) && len(b) <= 512 {
		return string(b)
	}
	enc := base64.StdEncoding.EncodeToString(b)
	if len(enc) > 128 {
		return enc[:128] + "…"
	}
	return enc
}

func isPrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x09 || (c > 0x0d && c < 0x20) {
			return false
		}
	}
	return true
}

func statusName(s commonv1.WorkflowStatus) string {
	switch s {
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_RUNNING:
		return "running"
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_COMPLETED:
		return "completed"
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_FAILED:
		return "failed"
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_TIMED_OUT:
		return "timedOut"
	case commonv1.WorkflowStatus_WORKFLOW_STATUS_TERMINATED:
		return "terminated"
	default:
		return "unspecified"
	}
}

func parseStatus(s string) commonv1.WorkflowStatus {
	switch s {
	case "running":
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_RUNNING
	case "completed":
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_COMPLETED
	case "failed":
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_FAILED
	case "timedOut":
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_TIMED_OUT
	case "terminated":
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_TERMINATED
	default:
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_UNSPECIFIED
	}
}

func eventTypeName(t commonv1.EventType) string {
	switch t {
	case commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
		return "WorkflowExecutionStarted"
	case commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
		return "ActivityTaskScheduled"
	case commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_STARTED:
		return "ActivityTaskStarted"
	case commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
		return "ActivityTaskCompleted"
	case commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_FAILED:
		return "ActivityTaskFailed"
	case commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_RETRY_SCHEDULED:
		return "ActivityTaskRetryScheduled"
	case commonv1.EventType_EVENT_TYPE_TIMER_STARTED:
		return "TimerStarted"
	case commonv1.EventType_EVENT_TYPE_TIMER_FIRED:
		return "TimerFired"
	case commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED:
		return "WorkflowExecutionSignaled"
	case commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
		return "WorkflowExecutionCompleted"
	case commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
		return "WorkflowExecutionFailed"
	default:
		return "Unspecified"
	}
}
