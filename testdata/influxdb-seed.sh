#!/bin/sh
# InfluxDB data seeding script for integration tests.
#
# Waits for InfluxDB to finish initial setup, then writes sample points using
# the v2 write API. Uses a fixed admin token so Grafana provisioning and the
# Go integration tests can both authenticate deterministically.

set -e

INFLUXDB_URL="${INFLUXDB_URL:-http://influxdb:8086}"
INFLUXDB_TOKEN="${INFLUXDB_TOKEN:-mcptests-admin-token}"
INFLUXDB_ORG="${INFLUXDB_ORG:-mcptests}"
INFLUXDB_BUCKET="${INFLUXDB_BUCKET:-metrics}"

echo "Waiting for InfluxDB to be ready at ${INFLUXDB_URL}..."
until curl -sf "${INFLUXDB_URL}/health" > /dev/null 2>&1; do
  sleep 2
done
echo "InfluxDB is ready."

# Generate 12 points spaced 30 minutes apart, ending "now" - that gives
# ~6 hours of seeded history so integration tests that look back 1-2h still
# find data even when run hours after the compose stack came up.
NOW_NS=$(date +%s)000000000
STEP_NS=1800000000000 # 30 minutes in nanoseconds
# Build a line-protocol payload with 12 cpu measurements and 12 mem measurements.
PAYLOAD=""
i=0
while [ $i -lt 12 ]; do
  TS=$((NOW_NS - i * STEP_NS))
  CPU_VAL=$((40 + i * 3))
  MEM_VAL=$((2048 + i * 16))
  PAYLOAD="${PAYLOAD}cpu,host=server1,region=us-east usage=${CPU_VAL} ${TS}
"
  PAYLOAD="${PAYLOAD}mem,host=server1,region=us-east used=${MEM_VAL} ${TS}
"
  i=$((i + 1))
done

curl -sf -X POST "${INFLUXDB_URL}/api/v2/write?org=${INFLUXDB_ORG}&bucket=${INFLUXDB_BUCKET}&precision=ns" \
  -H "Authorization: Token ${INFLUXDB_TOKEN}" \
  -H "Content-Type: text/plain; charset=utf-8" \
  --data-binary "${PAYLOAD}"
echo "Wrote sample points to bucket '${INFLUXDB_BUCKET}'."

# Map the bucket to a v1 database so InfluxQL queries work via the v2 server's
# v1-compat /query endpoint. Grafana's InfluxQL datasource uses this path.
curl -sf -X POST "${INFLUXDB_URL}/api/v2/dbrps" \
  -H "Authorization: Token ${INFLUXDB_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "bucketID": "'"$(curl -sf -G "${INFLUXDB_URL}/api/v2/buckets" \
      -H "Authorization: Token ${INFLUXDB_TOKEN}" \
      --data-urlencode "org=${INFLUXDB_ORG}" \
      --data-urlencode "name=${INFLUXDB_BUCKET}" \
      | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -n1)"'",
    "database": "metrics",
    "retention_policy": "autogen",
    "default": true,
    "org": "'"${INFLUXDB_ORG}"'"
  }' > /dev/null || echo "(dbrp mapping may already exist, continuing)"

echo "InfluxDB seeding complete."
