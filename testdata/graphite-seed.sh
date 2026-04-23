#!/bin/sh
# Graphite data seeding script for integration tests.
# Sends test metrics to Carbon via the plaintext protocol.

set -e

GRAPHITE_HOST="${GRAPHITE_HOST:-graphite}"
GRAPHITE_CARBON_PORT="${GRAPHITE_CARBON_PORT:-2003}"

echo "Waiting for Graphite Carbon to be ready on ${GRAPHITE_HOST}:${GRAPHITE_CARBON_PORT}..."
until nc -z "$GRAPHITE_HOST" "$GRAPHITE_CARBON_PORT" 2>/dev/null; do
  sleep 2
done
echo "Graphite Carbon is ready."

NOW=$(date +%s)

# Send all metrics in a single connection. Carbon processes batched writes
# over one TCP connection much more reliably than many short-lived ones.
printf "test.servers.web01.cpu.load5 1.5 %s\n\
test.servers.web01.cpu.load15 1.2 %s\n\
test.servers.web02.cpu.load5 2.3 %s\n\
test.servers.web02.cpu.load15 2.1 %s\n\
test.servers.db01.cpu.load5 0.8 %s\n\
test.tagged.cpu;server=web01;env=prod 1.5 %s\n\
test.tagged.cpu;server=web02;env=prod 2.3 %s\n" \
  "$NOW" "$NOW" "$NOW" "$NOW" "$NOW" "$NOW" "$NOW" \
  | nc -w 5 "$GRAPHITE_HOST" "$GRAPHITE_CARBON_PORT"

echo "Graphite metrics sent to Carbon."

# Wait until metrics are actually queryable via the Graphite web API.
# Carbon writes to Whisper files asynchronously, and /metrics/find requires
# the files to exist on disk, so a fixed sleep is unreliable on slow CI runners.
MAX_ATTEMPTS=60
attempt=0
echo "Waiting for metrics to be queryable via Graphite API..."
until wget -q -O - "http://${GRAPHITE_HOST}/metrics/find?query=test.*" 2>/dev/null | grep -q "test"; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge "$MAX_ATTEMPTS" ]; then
    echo "Timed out waiting for metrics to become queryable after ${MAX_ATTEMPTS} attempts."
    exit 1
  fi
  sleep 2
done
echo "Metrics are queryable. Done."
