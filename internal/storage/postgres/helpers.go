package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
)

// appendEvents assigns monotonic event ids from the run's next_event_id
// allocator and inserts each event, then advances the allocator. The caller
// must hold a row lock on the execution (SELECT ... FOR UPDATE) so the sequence
// is gap-free and race-free. It returns the id assigned to the first event.
func appendEvents(ctx context.Context, tx pgx.Tx, workflowID, runID string, events ...*commonv1.HistoryEvent) (int64, error) {
	if len(events) == 0 {
		return 0, nil
	}
	var nextID int64
	if err := tx.QueryRow(ctx,
		`SELECT next_event_id FROM workflow_executions
		 WHERE workflow_id = $1 AND run_id = $2::uuid FOR UPDATE`,
		workflowID, runID,
	).Scan(&nextID); err != nil {
		return 0, fmt.Errorf("load next_event_id: %w", err)
	}

	first := nextID
	for _, e := range events {
		e.EventId = nextID
		if e.EventTime == nil {
			e.EventTime = timestamppb.Now()
		}
		blob, err := proto.Marshal(e)
		if err != nil {
			return 0, fmt.Errorf("marshal event: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO history_events (workflow_id, run_id, event_id, event_type, event_time, data)
			 VALUES ($1, $2::uuid, $3, $4, $5, $6)`,
			workflowID, runID, e.EventId, int16(e.EventType), e.EventTime.AsTime(), blob,
		); err != nil {
			return 0, fmt.Errorf("insert event: %w", err)
		}
		nextID++
	}

	if _, err := tx.Exec(ctx,
		`UPDATE workflow_executions SET next_event_id = $3
		 WHERE workflow_id = $1 AND run_id = $2::uuid`,
		workflowID, runID, nextID,
	); err != nil {
		return 0, fmt.Errorf("advance next_event_id: %w", err)
	}
	return first, nil
}

// scheduleWorkflowTask enqueues a workflow task for the run, deduplicated so at
// most one pending task exists per run at any time.
func scheduleWorkflowTask(ctx context.Context, tx pgx.Tx, workflowID, runID, taskQueue string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO workflow_tasks (workflow_id, run_id, task_queue)
		 VALUES ($1, $2::uuid, $3)
		 ON CONFLICT (workflow_id, run_id) DO NOTHING`,
		workflowID, runID, taskQueue,
	)
	if err != nil {
		return fmt.Errorf("schedule workflow task: %w", err)
	}
	return nil
}

// loadHistory reads the full, ordered event history for a run.
func loadHistory(ctx context.Context, q pgx.Tx, workflowID, runID string) ([]*commonv1.HistoryEvent, error) {
	rows, err := q.Query(ctx,
		`SELECT data FROM history_events
		 WHERE workflow_id = $1 AND run_id = $2::uuid
		 ORDER BY event_id ASC`,
		workflowID, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()

	var events []*commonv1.HistoryEvent
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		e := &commonv1.HistoryEvent{}
		if err := proto.Unmarshal(blob, e); err != nil {
			return nil, fmt.Errorf("unmarshal event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// event constructs a HistoryEvent of the given type. event_id and event_time
// are assigned by appendEvents.
func event(t commonv1.EventType) *commonv1.HistoryEvent {
	return &commonv1.HistoryEvent{EventType: t}
}
