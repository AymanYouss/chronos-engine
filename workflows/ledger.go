// Package workflows contains the sample order-fulfillment workflow used by the
// docker-compose demo and the crash-and-resume scenario.
package workflows

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Ledger records activity side effects idempotently. It is the application-side
// half of the exactly-once story: the deterministic replay engine ensures a
// completed activity is never re-dispatched, and this ledger's primary key
// makes any at-least-once redelivery a no-op. The demo asserts that the number
// of ledger rows equals the number of distinct activities, i.e. zero
// duplicated side effects.
type Ledger struct {
	pool *pgxpool.Pool
}

// NewLedger builds a ledger over a connection pool.
func NewLedger(pool *pgxpool.Pool) *Ledger { return &Ledger{pool: pool} }

// Record persists a side effect keyed by (workflowID, activityID). Repeated
// calls with the same key are no-ops.
func (l *Ledger) Record(ctx context.Context, workflowID, activityID, activityType string, payload any) error {
	blob, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = l.pool.Exec(ctx,
		`INSERT INTO demo_side_effects (workflow_id, activity_id, activity_type, payload)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (workflow_id, activity_id) DO NOTHING`,
		workflowID, activityID, activityType, blob,
	)
	return err
}

// Count returns the number of distinct side effects recorded for a workflow.
func (l *Ledger) Count(ctx context.Context, workflowID string) (int, error) {
	var n int
	err := l.pool.QueryRow(ctx,
		`SELECT count(*) FROM demo_side_effects WHERE workflow_id = $1`, workflowID,
	).Scan(&n)
	return n, err
}
