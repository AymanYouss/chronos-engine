-- Chronos core schema.
--
-- The design is event-sourced: `history_events` is the immutable source of
-- truth for every workflow. All other tables are derived indexes or durable
-- queues that let stateless workers make progress and let a crashed execution
-- resume by replaying its history.

CREATE TABLE IF NOT EXISTS workflow_executions (
    workflow_id   TEXT        NOT NULL,
    run_id        UUID        NOT NULL,
    workflow_type TEXT        NOT NULL,
    task_queue    TEXT        NOT NULL,
    status        SMALLINT    NOT NULL,
    input         BYTEA,
    retry_policy  BYTEA,
    -- next_event_id is the sequence allocator for this run and the optimistic
    -- concurrency token: every history mutation advances it via compare-and-set.
    next_event_id BIGINT      NOT NULL DEFAULT 1,
    start_time    TIMESTAMPTZ NOT NULL DEFAULT now(),
    close_time    TIMESTAMPTZ,
    PRIMARY KEY (workflow_id, run_id)
);

-- At most one RUNNING execution may exist per workflow_id. This is what makes
-- StartWorkflow idempotent and prevents duplicate concurrent runs.
CREATE UNIQUE INDEX IF NOT EXISTS uq_workflow_running
    ON workflow_executions (workflow_id)
    WHERE status = 1;

CREATE INDEX IF NOT EXISTS ix_workflow_status_start
    ON workflow_executions (status, start_time DESC);

CREATE TABLE IF NOT EXISTS history_events (
    workflow_id TEXT        NOT NULL,
    run_id      UUID        NOT NULL,
    event_id    BIGINT      NOT NULL,
    event_type  SMALLINT    NOT NULL,
    event_time  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Serialized chronos.v1.HistoryEvent protobuf.
    data        BYTEA       NOT NULL,
    PRIMARY KEY (workflow_id, run_id, event_id),
    FOREIGN KEY (workflow_id, run_id)
        REFERENCES workflow_executions (workflow_id, run_id) ON DELETE CASCADE
);

-- Durable queue of workflow tasks. A workflow task tells a worker "there is new
-- history to process for this run". There is at most one pending workflow task
-- per run at a time (dedup via the partial unique index below).
CREATE TABLE IF NOT EXISTS workflow_tasks (
    task_id      BIGSERIAL   PRIMARY KEY,
    workflow_id  TEXT        NOT NULL,
    run_id       UUID        NOT NULL,
    task_queue   TEXT        NOT NULL,
    visible_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until TIMESTAMPTZ,
    locked_by    TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workflow_id, run_id)
        REFERENCES workflow_executions (workflow_id, run_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_workflow_task_pending
    ON workflow_tasks (workflow_id, run_id);

CREATE INDEX IF NOT EXISTS ix_workflow_task_dispatch
    ON workflow_tasks (task_queue, visible_at);

-- Durable queue of activity tasks. Exactly one row per scheduled activity
-- (keyed by scheduled_event_id); at-least-once dispatch with a visibility
-- timeout drives redelivery when a worker crashes mid-activity.
CREATE TABLE IF NOT EXISTS activity_tasks (
    task_id            BIGSERIAL   PRIMARY KEY,
    workflow_id        TEXT        NOT NULL,
    run_id             UUID        NOT NULL,
    scheduled_event_id BIGINT      NOT NULL,
    task_queue         TEXT        NOT NULL,
    activity_id        TEXT        NOT NULL,
    activity_type      TEXT        NOT NULL,
    input              BYTEA,
    retry_policy       BYTEA,
    attempt            INT         NOT NULL DEFAULT 1,
    visible_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until       TIMESTAMPTZ,
    locked_by          TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workflow_id, run_id)
        REFERENCES workflow_executions (workflow_id, run_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_activity_task
    ON activity_tasks (workflow_id, run_id, scheduled_event_id);

CREATE INDEX IF NOT EXISTS ix_activity_task_dispatch
    ON activity_tasks (task_queue, visible_at);

-- Durable timers. A background timer service fires due timers, appends
-- TIMER_FIRED to history, and schedules a workflow task.
CREATE TABLE IF NOT EXISTS timers (
    workflow_id      TEXT        NOT NULL,
    run_id           UUID        NOT NULL,
    started_event_id BIGINT      NOT NULL,
    timer_id         TEXT        NOT NULL,
    fire_at          TIMESTAMPTZ NOT NULL,
    fired            BOOLEAN     NOT NULL DEFAULT FALSE,
    PRIMARY KEY (workflow_id, run_id, started_event_id),
    FOREIGN KEY (workflow_id, run_id)
        REFERENCES workflow_executions (workflow_id, run_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS ix_timer_fire
    ON timers (fire_at)
    WHERE fired = FALSE;
