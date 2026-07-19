package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
)

// FireDueTimers fires every timer whose deadline has passed: it appends a
// TIMER_FIRED event and schedules a workflow task so the workflow resumes.
// Each timer is claimed with FOR UPDATE SKIP LOCKED so multiple control-plane
// replicas can run the timer service concurrently without double-firing.
func (s *Store) FireDueTimers(ctx context.Context, now time.Time, batch int) (int, error) {
	if batch <= 0 {
		batch = 100
	}
	fired := 0
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT workflow_id, run_id::text, started_event_id, timer_id
			 FROM timers
			 WHERE fired = FALSE AND fire_at <= $1
			 ORDER BY fire_at ASC
			 FOR UPDATE SKIP LOCKED
			 LIMIT $2`,
			now, batch,
		)
		if err != nil {
			return err
		}

		type due struct {
			workflowID     string
			runID          string
			startedEventID int64
			timerID        string
		}
		var dues []due
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.workflowID, &d.runID, &d.startedEventID, &d.timerID); err != nil {
				rows.Close()
				return err
			}
			dues = append(dues, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, d := range dues {
			ev := event(commonv1.EventType_EVENT_TYPE_TIMER_FIRED)
			ev.Attributes = &commonv1.HistoryEvent_TimerFired{
				TimerFired: &commonv1.TimerFiredAttributes{
					TimerId:        d.timerID,
					StartedEventId: d.startedEventID,
				},
			}
			if _, err := appendEvents(ctx, tx, d.workflowID, d.runID, ev); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE timers SET fired = TRUE
				 WHERE workflow_id = $1 AND run_id = $2::uuid AND started_event_id = $3`,
				d.workflowID, d.runID, d.startedEventID,
			); err != nil {
				return err
			}
			if err := scheduleWorkflowTaskForRun(ctx, tx, d.workflowID, d.runID); err != nil {
				return err
			}
			fired++
		}
		return nil
	})
	return fired, err
}
