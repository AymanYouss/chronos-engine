package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/internal/storage"
)

const statusRunning = int16(commonv1.WorkflowStatus_WORKFLOW_STATUS_RUNNING)

// StartWorkflow atomically creates the execution, writes the
// WORKFLOW_EXECUTION_STARTED event, and schedules the first workflow task. It
// is idempotent on workflow_id via a partial unique index on running rows.
func (s *Store) StartWorkflow(ctx context.Context, p storage.StartWorkflowParams) (string, bool, error) {
	runID := uuid.NewString()
	var policyBlob []byte
	if p.RetryPolicy != nil {
		b, err := proto.Marshal(p.RetryPolicy)
		if err != nil {
			return "", false, fmt.Errorf("marshal retry policy: %w", err)
		}
		policyBlob = b
	}

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO workflow_executions
			 (workflow_id, run_id, workflow_type, task_queue, status, input, retry_policy, next_event_id)
			 VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, 1)`,
			p.WorkflowID, runID, p.WorkflowType, p.TaskQueue, statusRunning, p.Input, policyBlob,
		)
		if err != nil {
			return err
		}

		started := event(commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_STARTED)
		started.Attributes = &commonv1.HistoryEvent_WorkflowExecutionStarted{
			WorkflowExecutionStarted: &commonv1.WorkflowExecutionStartedAttributes{
				WorkflowType: p.WorkflowType,
				TaskQueue:    p.TaskQueue,
				Input:        p.Input,
				RetryPolicy:  p.RetryPolicy,
			},
		}
		if _, err := appendEvents(ctx, tx, p.WorkflowID, runID, started); err != nil {
			return err
		}
		return scheduleWorkflowTask(ctx, tx, p.WorkflowID, runID, p.TaskQueue)
	})

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			existing, lookupErr := s.runningRunID(ctx, p.WorkflowID)
			if lookupErr != nil {
				return "", false, lookupErr
			}
			return existing, true, nil
		}
		return "", false, err
	}
	return runID, false, nil
}

func (s *Store) runningRunID(ctx context.Context, workflowID string) (string, error) {
	var runID string
	err := s.pool.QueryRow(ctx,
		`SELECT run_id::text FROM workflow_executions
		 WHERE workflow_id = $1 AND status = $2`,
		workflowID, statusRunning,
	).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", storage.ErrNotFound
	}
	return runID, err
}

// SignalWorkflow appends a signal event and schedules a workflow task so the
// workflow reacts on its next decision.
func (s *Store) SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, input []byte) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var status int16
		var taskQueue string
		err := tx.QueryRow(ctx,
			`SELECT status, task_queue FROM workflow_executions
			 WHERE workflow_id = $1 AND run_id = $2::uuid`,
			workflowID, runID,
		).Scan(&status, &taskQueue)
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != statusRunning {
			return fmt.Errorf("workflow not running")
		}

		sig := event(commonv1.EventType_EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED)
		sig.Attributes = &commonv1.HistoryEvent_WorkflowExecutionSignaled{
			WorkflowExecutionSignaled: &commonv1.WorkflowExecutionSignaledAttributes{
				SignalName: signalName,
				Input:      input,
			},
		}
		if _, err := appendEvents(ctx, tx, workflowID, runID, sig); err != nil {
			return err
		}
		return scheduleWorkflowTask(ctx, tx, workflowID, runID, taskQueue)
	})
}

// DescribeWorkflow returns projected execution state.
func (s *Store) DescribeWorkflow(ctx context.Context, workflowID, runID string) (*storage.Execution, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT workflow_id, run_id::text, workflow_type, task_queue, status,
		        start_time, close_time, next_event_id - 1
		 FROM workflow_executions
		 WHERE workflow_id = $1 AND run_id = $2::uuid`,
		workflowID, runID,
	)
	return scanExecution(row)
}

// GetHistory returns the full ordered history.
func (s *Store) GetHistory(ctx context.Context, workflowID, runID string) ([]*commonv1.HistoryEvent, error) {
	var events []*commonv1.HistoryEvent
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var e error
		events, e = loadHistory(ctx, tx, workflowID, runID)
		return e
	})
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, storage.ErrNotFound
	}
	return events, nil
}

// ListWorkflows returns executions, optionally filtered by status.
func (s *Store) ListWorkflows(ctx context.Context, statusFilter commonv1.WorkflowStatus, limit, offset int) ([]*storage.Execution, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var (
		rows pgx.Rows
		err  error
	)
	base := `SELECT workflow_id, run_id::text, workflow_type, task_queue, status,
	                start_time, close_time, next_event_id - 1
	         FROM workflow_executions`
	if statusFilter == commonv1.WorkflowStatus_WORKFLOW_STATUS_UNSPECIFIED {
		rows, err = s.pool.Query(ctx, base+
			` ORDER BY start_time DESC LIMIT $1 OFFSET $2`, limit, offset)
	} else {
		rows, err = s.pool.Query(ctx, base+
			` WHERE status = $1 ORDER BY start_time DESC LIMIT $2 OFFSET $3`,
			int16(statusFilter), limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.Execution
	for rows.Next() {
		exec, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, exec)
	}
	return out, rows.Err()
}

// CountByStatus returns a histogram of executions by status, used for metrics.
func (s *Store) CountByStatus(ctx context.Context) (map[commonv1.WorkflowStatus]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT status, count(*) FROM workflow_executions GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[commonv1.WorkflowStatus]int64)
	for rows.Next() {
		var status int16
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out[commonv1.WorkflowStatus(status)] = count
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanExecution(row scannable) (*storage.Execution, error) {
	var (
		e         storage.Execution
		status    int16
		closeTime *time.Time
	)
	err := row.Scan(&e.WorkflowID, &e.RunID, &e.WorkflowType, &e.TaskQueue,
		&status, &e.StartTime, &closeTime, &e.HistoryLength)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.Status = commonv1.WorkflowStatus(status)
	e.CloseTime = closeTime
	return &e, nil
}
