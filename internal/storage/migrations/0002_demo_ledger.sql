-- Application-side idempotency ledger used by the sample order-fulfillment
-- workflow. Each activity records its side effect keyed by (workflow_id,
-- activity_id). The PRIMARY KEY plus ON CONFLICT DO NOTHING makes every side
-- effect idempotent at the application boundary, so even an at-least-once
-- activity redelivery produces exactly one durable effect. The crash/resume
-- demo asserts row counts against this table.

CREATE TABLE IF NOT EXISTS demo_side_effects (
    workflow_id  TEXT        NOT NULL,
    activity_id  TEXT        NOT NULL,
    activity_type TEXT       NOT NULL,
    payload      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workflow_id, activity_id)
);

CREATE INDEX IF NOT EXISTS ix_demo_side_effects_wf
    ON demo_side_effects (workflow_id, created_at);
