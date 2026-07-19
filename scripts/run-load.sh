#!/usr/bin/env bash
#
# run-load.sh [COUNT] [WORKERS]
#
# Starts COUNT order-fulfillment workflows against a running stack and reports
# end-to-end completion throughput. Scale WORKERS to watch throughput rise.
#
#   ./scripts/run-load.sh 500 4
#
# Requires the stack to be up (docker compose up -d) and PackagingDelay to be
# small; set CHRONOS_PACKAGING_DELAY=0s on the workers for a pure throughput run.
set -euo pipefail
cd "$(dirname "$0")/.."

COUNT="${1:-200}"
WORKERS="${2:-2}"
COMPOSE="docker compose"
PSQL="$COMPOSE exec -T postgres psql -U chronos -d chronos -tA"
CTL() { $COMPOSE exec -T -e CHRONOS_SERVER=localhost:7233 server /usr/local/bin/chronosctl "$@"; }

echo "==> Scaling workers to $WORKERS"
$COMPOSE up -d --scale worker="$WORKERS" worker

RUN_TAG="load-$(date +%s)"
echo "==> Launching $COUNT workflows (tag $RUN_TAG)"
START=$(date +%s.%N)
for i in $(seq 1 "$COUNT"); do
  CTL start -id "${RUN_TAG}-${i}" -type OrderFulfillment -queue default \
    -input '{"orderId":"'"${RUN_TAG}-${i}"'","amountCents":100,"items":["x"]}' >/dev/null &
  if (( i % 25 == 0 )); then wait; fi
done
wait

echo "==> Waiting for all workflows to complete"
for _ in $(seq 1 600); do
  DONE="$($PSQL -c "SELECT count(*) FROM workflow_executions WHERE workflow_id LIKE '${RUN_TAG}-%' AND status=2;" | tr -d '[:space:]')"
  [ "${DONE:-0}" -ge "$COUNT" ] && break
  sleep 0.5
done
END=$(date +%s.%N)

ELAPSED=$(echo "$END - $START" | bc)
THROUGHPUT=$(echo "scale=1; $COUNT / $ELAPSED" | bc)
echo "==> Completed $DONE/$COUNT workflows in ${ELAPSED}s"
echo "==> Throughput: ${THROUGHPUT} workflows/sec with $WORKERS workers"
