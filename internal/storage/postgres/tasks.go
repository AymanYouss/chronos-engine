package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/internal/core"
	"github.com/AymanYouss/chronos-engine/internal/storage"
)

// PollWorkflowTask leases the next available workflow task on the queue using
// SELECT ... FOR UPDATE SKIP LOCKED, which lets many workers poll concurrently
// without ever handing the same task to two workers. It returns (nil, nil) when
// the queue is empty so the caller can long-poll.
func (s *Store) PollWorkflowTask(ctx context.Context, taskQueue, identity string, lease time.Duration) (*core.WorkflowTask, error) {
	var task *core.WorkflowTask
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			taskID     int64
			workflowID string
			runID      string
		)
		err := tx.QueryRow(ctx,
			`SELECT task_id, workflow_id, run_id::text
			 FROM workflow_tasks
			 WHERE task_queue = $1
			   AND visible_at <= now()
			   AND (locked_until IS NULL OR locked_until <= now())
			 ORDER BY visible_at ASC
			 FOR UPDATE SKIP LOCKED
			 LIMIT 1`, taskQueue,
		).Scan(&taskID, &workflowID, &runID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx,
			`UPDATE workflow_tasks SET locked_until = now() + $2, locked_by = $3
			 WHERE task_id = $1`,
			taskID, lease, identity,
		); err != nil {
			return err
		}

		var wfType string
		var nextEventID int64
		if err := tx.QueryRow(ctx,
			`SELECT workflow_type, next_event_id FROM workflow_executions
			 WHERE workflow_id = $1 AND run_id = $2::uuid`,
			workflowID, runID,
		).Scan(&wfType, &nextEventID); err != nil {
			return err
		}

		history, err := loadHistory(ctx, tx, workflowID, runID)
		if err != nil {
			return err
		}

		task = &core.WorkflowTask{
			TaskToken:      taskID,
			WorkflowID:     workflowID,
			RunID:          runID,
			Type:           wfType,
			History:        history,
			StartedEventID: nextEventID - 1,
		}
		return nil
	})
	return task, err
}

// CompleteWorkflowTask applies the commands produced by one deterministic
// workflow decision as new history events, atomically. Ownership is verified
// against the lease (locked_by), so a duplicate delivery completed after the
// original is rejected with ErrTaskLost and produces no duplicate events.
func (s *Store) CompleteWorkflowTask(ctx context.Context, token int64, identity string, commands []*commonv1.Command) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			workflowID string
			runID      string
			taskQueue  string
			lockedBy   *string
		)
		err := tx.QueryRow(ctx,
			`SELECT workflow_id, run_id::text, task_queue, locked_by
			 FROM workflow_tasks WHERE task_id = $1 FOR UPDATE`, token,
		).Scan(&workflowID, &runID, &taskQueue, &lockedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrTaskLost
		}
		if err != nil {
			return err
		}
		if lockedBy == nil || *lockedBy != identity {
			return storage.ErrTaskLost
		}

		terminal := false
		for _, cmd := range commands {
			if terminal {
				break
			}
			switch a := cmd.Attributes.(type) {
			case *commonv1.Command_ScheduleActivityTask:
				if err := applyScheduleActivity(ctx, tx, workflowID, runID, a.ScheduleActivityTask); err != nil {
					return err
				}
			case *commonv1.Command_StartTimer:
				if err := applyStartTimer(ctx, tx, workflowID, runID, a.StartTimer); err != nil {
					return err
				}
			case *commonv1.Command_CompleteWorkflowExecution:
				if err := applyCompleteWorkflow(ctx, tx, workflowID, runID, a.CompleteWorkflowExecution.Result); err != nil {
					return err
				}
				terminal = true
			case *commonv1.Command_FailWorkflowExecution:
				if err := applyFailWorkflow(ctx, tx, workflowID, runID, a.FailWorkflowExecution.Failure); err != nil {
					return err
				}
				terminal = true
			default:
				return fmt.Errorf("unknown command type %T", cmd.Attributes)
			}
		}

		_, err = tx.Exec(ctx, `DELETE FROM workflow_tasks WHERE task_id = $1`, token)
		return err
	})
}

func applyScheduleActivity(ctx context.Context, tx pgx.Tx, workflowID, runID string, cmd *commonv1.ScheduleActivityTaskCommand) error {
	retry := cmd.RetryPolicy
	if retry == nil {
		retry = core.DefaultActivityRetryPolicy()
	}
	ev := event(commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_SCHEDULED)
	ev.Attributes = &commonv1.HistoryEvent_ActivityTaskScheduled{
		ActivityTaskScheduled: &commonv1.ActivityTaskScheduledAttributes{
			ActivityId:          cmd.ActivityId,
			ActivityType:        cmd.ActivityType,
			TaskQueue:           cmd.TaskQueue,
			Input:               cmd.Input,
			RetryPolicy:         retry,
			StartToCloseTimeout: cmd.StartToCloseTimeout,
			Attempt:             1,
		},
	}
	scheduledID, err := appendEvents(ctx, tx, workflowID, runID, ev)
	if err != nil {
		return err
	}
	var policyBlob []byte
	if b, err := proto.Marshal(retry); err == nil {
		policyBlob = b
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO activity_tasks
		 (workflow_id, run_id, scheduled_event_id, task_queue, activity_id, activity_type, input, retry_policy, attempt)
		 VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, 1)`,
		workflowID, runID, scheduledID, cmd.TaskQueue, cmd.ActivityId, cmd.ActivityType, cmd.Input, policyBlob,
	)
	return err
}

func applyStartTimer(ctx context.Context, tx pgx.Tx, workflowID, runID string, cmd *commonv1.StartTimerCommand) error {
	fireAt := time.Now().Add(cmd.StartToFireTimeout.AsDuration())
	ev := event(commonv1.EventType_EVENT_TYPE_TIMER_STARTED)
	ev.Attributes = &commonv1.HistoryEvent_TimerStarted{
		TimerStarted: &commonv1.TimerStartedAttributes{
			TimerId:            cmd.TimerId,
			StartToFireTimeout: cmd.StartToFireTimeout,
			FireTime:           timestamppb.New(fireAt),
		},
	}
	startedID, err := appendEvents(ctx, tx, workflowID, runID, ev)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO timers (workflow_id, run_id, started_event_id, timer_id, fire_at)
		 VALUES ($1, $2::uuid, $3, $4, $5)`,
		workflowID, runID, startedID, cmd.TimerId, fireAt,
	)
	return err
}

func applyCompleteWorkflow(ctx context.Context, tx pgx.Tx, workflowID, runID string, result []byte) error {
	ev := event(commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED)
	ev.Attributes = &commonv1.HistoryEvent_WorkflowExecutionCompleted{
		WorkflowExecutionCompleted: &commonv1.WorkflowExecutionCompletedAttributes{Result: result},
	}
	if _, err := appendEvents(ctx, tx, workflowID, runID, ev); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`UPDATE workflow_executions SET status = $3, close_time = now()
		 WHERE workflow_id = $1 AND run_id = $2::uuid`,
		workflowID, runID, int16(commonv1.WorkflowStatus_WORKFLOW_STATUS_COMPLETED),
	)
	return err
}

func applyFailWorkflow(ctx context.Context, tx pgx.Tx, workflowID, runID string, failure *commonv1.Failure) error {
	ev := event(commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_FAILED)
	ev.Attributes = &commonv1.HistoryEvent_WorkflowExecutionFailed{
		WorkflowExecutionFailed: &commonv1.WorkflowExecutionFailedAttributes{Failure: failure},
	}
	if _, err := appendEvents(ctx, tx, workflowID, runID, ev); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`UPDATE workflow_executions SET status = $3, close_time = now()
		 WHERE workflow_id = $1 AND run_id = $2::uuid`,
		workflowID, runID, int16(commonv1.WorkflowStatus_WORKFLOW_STATUS_FAILED),
	)
	return err
}

// PollActivityTask leases the next activity task on the queue. Redelivery of a
// crashed worker's task happens automatically once its lease (locked_until)
// expires, giving at-least-once dispatch.
func (s *Store) PollActivityTask(ctx context.Context, taskQueue, identity string, lease time.Duration) (*core.ActivityTask, error) {
	var task *core.ActivityTask
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			taskID           int64
			workflowID       string
			runID            string
			scheduledEventID int64
			activityID       string
			activityType     string
			input            []byte
			attempt          int32
		)
		err := tx.QueryRow(ctx,
			`SELECT task_id, workflow_id, run_id::text, scheduled_event_id,
			        activity_id, activity_type, input, attempt
			 FROM activity_tasks
			 WHERE task_queue = $1
			   AND visible_at <= now()
			   AND (locked_until IS NULL OR locked_until <= now())
			 ORDER BY visible_at ASC
			 FOR UPDATE SKIP LOCKED
			 LIMIT 1`, taskQueue,
		).Scan(&taskID, &workflowID, &runID, &scheduledEventID, &activityID, &activityType, &input, &attempt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx,
			`UPDATE activity_tasks SET locked_until = now() + $2, locked_by = $3
			 WHERE task_id = $1`,
			taskID, lease, identity,
		); err != nil {
			return err
		}

		task = &core.ActivityTask{
			TaskToken:        taskID,
			WorkflowID:       workflowID,
			RunID:            runID,
			ActivityID:       activityID,
			ActivityType:     activityType,
			Input:            input,
			Attempt:          attempt,
			ScheduledEventID: scheduledEventID,
		}
		return nil
	})
	return task, err
}

// CompleteActivityTask records the result exactly once (deleting the queue row
// so any redelivered duplicate is rejected) and schedules a workflow task to
// advance the workflow.
func (s *Store) CompleteActivityTask(ctx context.Context, token int64, identity string, result []byte) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			workflowID       string
			runID            string
			scheduledEventID int64
			lockedBy         *string
		)
		err := tx.QueryRow(ctx,
			`SELECT workflow_id, run_id::text, scheduled_event_id, locked_by
			 FROM activity_tasks WHERE task_id = $1 FOR UPDATE`, token,
		).Scan(&workflowID, &runID, &scheduledEventID, &lockedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrTaskLost
		}
		if err != nil {
			return err
		}
		if lockedBy == nil || *lockedBy != identity {
			return storage.ErrTaskLost
		}

		ev := event(commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_COMPLETED)
		ev.Attributes = &commonv1.HistoryEvent_ActivityTaskCompleted{
			ActivityTaskCompleted: &commonv1.ActivityTaskCompletedAttributes{
				ScheduledEventId: scheduledEventID,
				Result:           result,
			},
		}
		if _, err := appendEvents(ctx, tx, workflowID, runID, ev); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM activity_tasks WHERE task_id = $1`, token); err != nil {
			return err
		}
		return scheduleWorkflowTaskForRun(ctx, tx, workflowID, runID)
	})
}

// FailActivityTask applies the activity retry policy: it either reschedules the
// task with exponential backoff (recording ACTIVITY_TASK_RETRY_SCHEDULED) or,
// once retries are exhausted, records a terminal ACTIVITY_TASK_FAILED and lets
// the workflow observe the failure.
func (s *Store) FailActivityTask(ctx context.Context, token int64, identity string, failure *commonv1.Failure, retryable bool) (bool, error) {
	retried := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			workflowID       string
			runID            string
			scheduledEventID int64
			attempt          int32
			policyBlob       []byte
			lockedBy         *string
		)
		err := tx.QueryRow(ctx,
			`SELECT workflow_id, run_id::text, scheduled_event_id, attempt, retry_policy, locked_by
			 FROM activity_tasks WHERE task_id = $1 FOR UPDATE`, token,
		).Scan(&workflowID, &runID, &scheduledEventID, &attempt, &policyBlob, &lockedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrTaskLost
		}
		if err != nil {
			return err
		}
		if lockedBy == nil || *lockedBy != identity {
			return storage.ErrTaskLost
		}

		policy := core.DefaultActivityRetryPolicy()
		if len(policyBlob) > 0 {
			p := &commonv1.RetryPolicy{}
			if err := proto.Unmarshal(policyBlob, p); err == nil {
				policy = p
			}
		}

		delay, canRetry := core.NextRetryDelay(policy, attempt)
		if retryable && canRetry {
			next := attempt + 1
			nextTime := time.Now().Add(delay)
			ev := event(commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_RETRY_SCHEDULED)
			ev.Attributes = &commonv1.HistoryEvent_ActivityTaskRetryScheduled{
				ActivityTaskRetryScheduled: &commonv1.ActivityTaskRetryScheduledAttributes{
					ScheduledEventId: scheduledEventID,
					Attempt:          next,
					NextAttemptTime:  timestamppb.New(nextTime),
					Failure:          failure,
				},
			}
			if _, err := appendEvents(ctx, tx, workflowID, runID, ev); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE activity_tasks
				 SET attempt = $2, visible_at = $3, locked_until = NULL, locked_by = NULL
				 WHERE task_id = $1`,
				token, next, nextTime,
			); err != nil {
				return err
			}
			retried = true
			return nil
		}

		ev := event(commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_FAILED)
		ev.Attributes = &commonv1.HistoryEvent_ActivityTaskFailed{
			ActivityTaskFailed: &commonv1.ActivityTaskFailedAttributes{
				ScheduledEventId: scheduledEventID,
				Failure:          failure,
				Retryable:        retryable,
			},
		}
		if _, err := appendEvents(ctx, tx, workflowID, runID, ev); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM activity_tasks WHERE task_id = $1`, token); err != nil {
			return err
		}
		return scheduleWorkflowTaskForRun(ctx, tx, workflowID, runID)
	})
	return retried, err
}

// ExtendActivityLease pushes out the visibility timeout for an in-flight
// activity so long-running work is not redelivered while it makes progress.
func (s *Store) ExtendActivityLease(ctx context.Context, token int64, lease time.Duration) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE activity_tasks SET locked_until = now() + $2
		 WHERE task_id = $1 AND locked_until IS NOT NULL AND locked_until > now()`,
		token, lease,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// scheduleWorkflowTaskForRun looks up the run's task queue and enqueues a
// workflow task.
func scheduleWorkflowTaskForRun(ctx context.Context, tx pgx.Tx, workflowID, runID string) error {
	var taskQueue string
	if err := tx.QueryRow(ctx,
		`SELECT task_queue FROM workflow_executions
		 WHERE workflow_id = $1 AND run_id = $2::uuid`,
		workflowID, runID,
	).Scan(&taskQueue); err != nil {
		return err
	}
	return scheduleWorkflowTask(ctx, tx, workflowID, runID, taskQueue)
}
