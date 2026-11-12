#!/bin/sh
# Seeds a secondary organization and a dashboard inside it on both the modern
# and legacy Grafana instances, giving the orgId-parameter integration tests a
# multi-org fixture to read.
#
# Neither the org nor the per-org dashboard can come from file-based
# provisioning: a dashboard provider's org_id must reference an org that already
# exists, provisioning runs once at startup, and there is no file provisioning
# for orgs themselves. So we POST both once Grafana is healthy, like the other
# *-seed jobs. Creating the org also enrolls the creating admin as a member
# (CreateOrg uses CreateWithMember), so admin:admin can write into it directly.
set -e

ORG_NAME="${ORG_NAME:-mcp-orgid-test}"

# seed_instance <base-url> <dashboard-uid> <dashboard-title>
seed_instance() {
  base="$1"
  uid="$2"
  title="$3"

  # Create the org (ignore "name taken" across restarts) and resolve its id.
  curl -s -u admin:admin -X POST "$base/api/orgs" \
    -H "Content-Type: application/json" -d "{\"name\":\"$ORG_NAME\"}" >/dev/null || true
  org_id=$(curl -sf -u admin:admin "$base/api/orgs/name/$ORG_NAME" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  if [ -z "$org_id" ]; then
    echo "failed to resolve org id for $ORG_NAME on $base" >&2
    exit 1
  fi

  # Create the dashboard in that org (overwrite makes it idempotent).
  curl -sf -u admin:admin -H "X-Grafana-Org-Id: $org_id" \
    -X POST "$base/api/dashboards/db" \
    -H "Content-Type: application/json" \
    -d "{\"overwrite\":true,\"dashboard\":{\"uid\":\"$uid\",\"title\":\"$title\",\"schemaVersion\":39,\"tags\":[\"mcp-orgid-int\"],\"panels\":[]}}" >/dev/null

  echo "seeded org $ORG_NAME (id=$org_id) with dashboard $uid on $base"
}

seed_instance "${GRAFANA_URL:-http://grafana:3000}" "mcp-orgid-ns" "OrgID NS Dashboard"
seed_instance "${LEGACY_GRAFANA_URL:-http://grafana-legacy:3000}" "mcp-orgid-legacy" "OrgID Legacy Dashboard"

# Seed a Tempo datasource into the secondary org on the modern instance, so the
# proxied-tools multi-org integration test can discover an MCP datasource that
# only exists in a non-default org and route to it via orgId. It points at the
# same Tempo backend (which serves MCP) as the default org's Tempo.
seed_org2_tempo() {
  base="$1"
  org_id=$(curl -sf -u admin:admin "$base/api/orgs/name/$ORG_NAME" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  if [ -z "$org_id" ]; then
    return 0
  fi
  curl -sf -u admin:admin -H "X-Grafana-Org-Id: $org_id" \
    -X POST "$base/api/datasources" -H "Content-Type: application/json" \
    -d '{"name":"Tempo Org2","uid":"tempo-org2","type":"tempo","access":"proxy","url":"http://tempo:3200"}' >/dev/null 2>&1 || true
  echo "seeded tempo datasource (tempo-org2) in org $ORG_NAME (id=$org_id) on $base"
}

seed_org2_tempo "${GRAFANA_URL:-http://grafana:3000}"
