# Chronos architecture

Chronos is a durable-execution engine: it runs ordinary code that survives process crashes, redeploys, and
infrastructure failures. This document explains how.

## 1. The model

Two kinds of code run on Chronos:

- **Workflows** are *deterministic orchestrators*. They decide what happens and in what order, but they may
  not perform side effects directly. Given the same history, a workflow always makes the same decisions.
- **Activities** are *non-deterministic side effects* — HTTP calls, DB writes, payments. They can do anything,
  and they can fail and be retried.

A running workflow is not a live process holding state in memory. It is an **append-only event history** in
Postgres. State is reconstructed on demand by replaying that history through the workflow function. This is
the source of durability: there is no in-memory state to lose.

## 2. Event sourcing

Every meaningful thing that happens is an immutable `HistoryEvent` with a per-workflow monotonic `event_id`:

```
WorkflowExecutionStarted
ActivityTaskScheduled / ActivityTaskStarted / ActivityTaskCompleted / ActivityTaskFailed / ActivityTaskRetryScheduled
TimerStarted / TimerFired
WorkflowExecutionSignaled
WorkflowExecutionCompleted / WorkflowExecutionFailed
```

The history is the single source of truth. `workflow_executions` (status, timing) is a *projection* kept in
the same transaction as the events, and `next_event_id` on that row is both the sequence allocator and the
optimistic-concurrency token.

## 3. Deterministic replay — the core

When a worker receives a workflow task, it gets the full history and re-executes the workflow function from
the top. The trick is what happens at each `ExecuteActivity` / `NewTimer` call:

1. The engine indexes the history into ordered projections of activities and timers, resolving which ones have
   completed.
2. It runs the workflow function once. The *n*-th `ExecuteActivity` call is matched to the *n*-th
   `ActivityTaskScheduled` event:
   - **Completed in history** → the `Future` resolves immediately with the recorded result. *The activity is
     not run again.*
   - **Scheduled but not complete** → the `Future` is pending.
   - **Not in history yet** → the engine emits a new `ScheduleActivityTask` **command**.
3. Calling `Get()` on a pending `Future` cannot make progress this turn, so execution unwinds (via a panic/
   recover sentinel — a blocked workflow never resumes within the same decision, so there is no goroutine
   scheduler to maintain). The commands accumulated so far are the workflow's decision.

Because the mapping from history to commands is pure, **replaying identical history always yields identical
commands.** That is determinism, and it is what makes crash-resume exact: after a crash, the new worker sees
the same history and continues as if nothing happened. Completed activities are read from history, so their
side effects never repeat. If workflow code is changed incompatibly, the mismatch is detected and surfaced as
a nondeterminism error rather than silently corrupting state.

```
task 1: [Started]                                  → schedule ChargePayment
task 2: [.. ChargePayment done ..]                 → start packaging timer
        ── worker crashes here; another worker polls the pending task ──
task 3: [.. timer fired ..]                         → schedule ShipOrder   (ChargePayment NOT re-run)
task 4: [.. ShipOrder done ..]                      → complete workflow
```

## 4. Exactly-once activities

Chronos gives the classic durable-execution guarantee: **activity results are recorded into history exactly
once**, and **completed activities are never re-dispatched**. Two mechanisms combine:

1. **At-least-once dispatch with idempotent recording.** Activity tasks live in the `activity_tasks` queue
   with a visibility timeout. A worker leases a task (`SELECT … FOR UPDATE SKIP LOCKED`), runs it, and reports
   the result. Recording the result appends `ActivityTaskCompleted` **and deletes the queue row in the same
   transaction**, and the completion is only accepted if the caller still holds the lease. A duplicate
   delivery completed after the original finds no row and is rejected — so exactly one completion event is
   ever written.
2. **Deterministic replay.** Once an activity is completed in history, replay serves its result from history
   and the workflow never schedules it again.

For the (rare) window where a worker crashes *after* a side effect but *before* reporting, the task is
redelivered and the activity runs again. Application code makes this safe with an idempotency key; the sample
workflow uses a `demo_side_effects` ledger keyed by `(workflow_id, activity_id)` with `ON CONFLICT DO
NOTHING`. The crash-resume demo asserts the ledger holds exactly one row per activity.

## 5. Durable task queues

Both queues are plain Postgres tables, which keeps the whole system to a single stateful dependency:

- `workflow_tasks` — at most one pending task per run (a partial unique index dedupes). Scheduled whenever
  history advances (activity completed, timer fired, signal received).
- `activity_tasks` — one row per scheduled activity, carrying attempt count and retry policy.

Polling uses `SELECT … FOR UPDATE SKIP LOCKED`, so any number of workers (and any number of control-plane
replicas) can poll concurrently without ever handing the same task to two workers. A crashed worker's lease
simply expires and the task becomes visible again — this is what drives automatic redelivery.

## 6. Durable timers

`StartTimer` writes a `timers` row with a `fire_at` deadline. A timer service in the control plane scans due
timers (again with `FOR UPDATE SKIP LOCKED`, so replicas cooperate), appends `TimerFired`, and schedules a
workflow task. Timers therefore survive restarts and fire even if every worker is down at the deadline.

## 7. Retries

When an activity fails, the store consults its retry policy. If attempts remain, it appends
`ActivityTaskRetryScheduled` and makes the task visible again after an exponential-backoff delay
(`initial × coefficientⁿ`, clamped to a max). When attempts are exhausted (or the failure is non-retryable),
it appends a terminal `ActivityTaskFailed` and schedules a workflow task so the workflow observes the error
and can react.

## 8. Scaling & operations

- **Control plane** is stateless and horizontally scalable; run several replicas behind the gRPC service.
- **Workers** are stateless; scale them with an HPA. Throughput rises linearly until Postgres saturates.
- **Observability:** every component exports Prometheus metrics (workflow/activity/timer counters, task
  dispatch rates, replay-latency histogram, executions-by-status gauges) with a ready-made Grafana dashboard.
- **Storage:** a single Postgres (RDS Multi-AZ in the provided Terraform) is the durability boundary.
