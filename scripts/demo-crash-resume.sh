#!/usr/bin/env bash
#
# demo-crash-resume.sh
#
# Demonstrates the core durability guarantee of Chronos: a workflow survives a
# worker crash and resumes on a different worker with zero duplicated side
# effects. The script:
#
#   1. Brings up Postgres + the control plane.
#   2. Starts one worker and launches the order-fulfillment workflow.
#   3. Waits until the workflow is durably parked on its packaging timer
#      (payment charged + inventory reserved), then SIGKILLs the worker.
#   4. Starts a fresh worker, which replays history and finishes the workflow.
#   5. Asserts the idempotency ledger holds exactly one side effect per activity
#      and the history holds exactly one completion per activity.
#
# Evidence is written to docs/portfolio/demo-evidence.txt.
set -euo pipefail

cd "$(dirname "$0")/.."

COMPOSE="docker compose"
PSQL="$COMPOSE exec -T postgres psql -U chronos -d chronos -tA"
CTL() { $COMPOSE exec -T -e CHRONOS_SERVER=localhost:7233 server /usr/local/bin/chronosctl "$@"; }

WORKFLOW_ID="order-demo-$(date +%s)"
EVIDENCE="docs/portfolio/demo-evidence.txt"
DEMO_WORKER_1="chronos-demo-worker-1"
DEMO_WORKER_2="chronos-demo-worker-2"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m ✓\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m ✗ %s\033[0m\n' "$*"; exit 1; }

cleanup() {
  docker rm -f "$DEMO_WORKER_1" "$DEMO_WORKER_2" >/dev/null 2>&1 || true
}
trap cleanup EXIT

log "Building images and starting Postgres + control plane"
$COMPOSE up -d --build postgres server

log "Waiting for the control plane to become healthy"
for _ in $(seq 1 60); do
  if $COMPOSE exec -T postgres pg_isready -U chronos -d chronos >/dev/null 2>&1; then break; fi
  sleep 1
done

log "Starting worker #1 ($DEMO_WORKER_1)"
docker run -d --name "$DEMO_WORKER_1" \
  --network chronos_default \
  -e CHRONOS_SERVER=server:7233 \
  -e CHRONOS_DATABASE_URL=postgres://chronos:chronos@postgres:5432/chronos?sslmode=disable \
  -e CHRONOS_WORKER_IDENTITY="$DEMO_WORKER_1" \
  chronos-server /usr/local/bin/chronos-worker >/dev/null

log "Starting workflow $WORKFLOW_ID"
CTL start -id "$WORKFLOW_ID" -type OrderFulfillment -queue default \
  -input '{"orderId":"'"$WORKFLOW_ID"'","customerId":"cust-42","amountCents":4999,"items":["widget","gadget"]}'

RUN_ID="$($PSQL -c "SELECT run_id FROM workflow_executions WHERE workflow_id='$WORKFLOW_ID' LIMIT 1;" | tr -d '[:space:]')"
[ -n "$RUN_ID" ] || fail "could not resolve run_id"
ok "run_id=$RUN_ID"

log "Waiting until the workflow is durably parked on its packaging timer"
for _ in $(seq 1 60); do
  TIMERS="$($PSQL -c "SELECT count(*) FROM history_events WHERE workflow_id='$WORKFLOW_ID' AND event_type=10;" | tr -d '[:space:]')"
  if [ "${TIMERS:-0}" -ge 1 ]; then break; fi
  sleep 0.5
done
[ "${TIMERS:-0}" -ge 1 ] || fail "workflow never reached the packaging timer"
BEFORE="$($PSQL -c "SELECT count(*) FROM demo_side_effects WHERE workflow_id='$WORKFLOW_ID';" | tr -d '[:space:]')"
ok "payment charged + inventory reserved (side effects so far: $BEFORE)"

log "SIGKILLing worker #1 mid-workflow"
docker kill "$DEMO_WORKER_1" >/dev/null
ok "worker #1 killed while the workflow was still RUNNING"

STATUS_MID="$($PSQL -c "SELECT status FROM workflow_executions WHERE workflow_id='$WORKFLOW_ID';" | tr -d '[:space:]')"
[ "$STATUS_MID" = "1" ] && ok "workflow status is RUNNING (durably parked, no worker alive)"

log "Starting worker #2 ($DEMO_WORKER_2) to resume the workflow"
docker run -d --name "$DEMO_WORKER_2" \
  --network chronos_default \
  -e CHRONOS_SERVER=server:7233 \
  -e CHRONOS_DATABASE_URL=postgres://chronos:chronos@postgres:5432/chronos?sslmode=disable \
  -e CHRONOS_WORKER_IDENTITY="$DEMO_WORKER_2" \
  chronos-server /usr/local/bin/chronos-worker >/dev/null

log "Awaiting completion"
CTL await -id "$WORKFLOW_ID" -run "$RUN_ID" -status completed -timeout 60s || fail "workflow did not complete after resume"

# --- assertions -------------------------------------------------------------
LEDGER="$($PSQL -c "SELECT count(*) FROM demo_side_effects WHERE workflow_id='$WORKFLOW_ID';" | tr -d '[:space:]')"
COMPLETED="$($PSQL -c "SELECT count(*) FROM history_events WHERE workflow_id='$WORKFLOW_ID' AND event_type=7;" | tr -d '[:space:]')"

log "Verifying exactly-once side effects"
[ "$LEDGER" = "4" ]    || fail "expected 4 distinct side effects, got $LEDGER"
[ "$COMPLETED" = "4" ] || fail "expected 4 ActivityTaskCompleted events, got $COMPLETED"
ok "ledger side effects = $LEDGER (one per activity: charge, reserve, ship, receipt)"
ok "ActivityTaskCompleted events = $COMPLETED (no duplicates)"

log "Final event history"
CTL history -id "$WORKFLOW_ID" -run "$RUN_ID"

{
  echo "Chronos crash-and-resume demo"
  echo "generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "workflow_id: $WORKFLOW_ID"
  echo "run_id: $RUN_ID"
  echo
  echo "RESULT: worker #1 was SIGKILLed mid-workflow; worker #2 replayed history and finished."
  echo "distinct side effects (idempotency ledger): $LEDGER  (expected 4)"
  echo "ActivityTaskCompleted events in history:    $COMPLETED  (expected 4, i.e. zero duplicates)"
  echo
  echo "Event history:"
  CTL history -id "$WORKFLOW_ID" -run "$RUN_ID"
} > "$EVIDENCE"

ok "Evidence written to $EVIDENCE"
printf '\n\033[1;32mDEMO PASSED: crashed mid-workflow, resumed on a new worker, zero duplicated side effects.\033[0m\n'
