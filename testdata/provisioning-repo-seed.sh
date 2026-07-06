#!/bin/sh
# Registers a git-sync provisioning repository pointing at the local gitserver.
# Provisioning repositories are Kubernetes-style resources created through the
# provisioning.grafana.app API; there is no file-based provisioning for them,
# so we POST the resource once Grafana is up (mirroring the other *-seed jobs).
set -e

GRAFANA_URL="${GRAFANA_URL:-http://grafana:3000}"
REPO_NAME="${REPO_NAME:-test-repo}"
GIT_URL="${GIT_URL:-http://gitserver/git/test-repo.git}"
API="$GRAFANA_URL/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories"

# If the repository already exists (Grafana persisted it across restarts), do
# nothing — POSTing again would 409.
if curl -sf -u admin:admin "$API/$REPO_NAME" >/dev/null 2>&1; then
  echo "repository $REPO_NAME already exists, skipping"
  exit 0
fi

curl -sf -u admin:admin -X POST "$API" \
  -H "Content-Type: application/json" \
  -d "{
    \"apiVersion\": \"provisioning.grafana.app/v0alpha1\",
    \"kind\": \"Repository\",
    \"metadata\": { \"name\": \"$REPO_NAME\", \"namespace\": \"default\" },
    \"spec\": {
      \"title\": \"Integration Test Repo\",
      \"type\": \"git\",
      \"sync\": { \"enabled\": true, \"target\": \"folder\", \"intervalSeconds\": 60 },
      \"git\": { \"url\": \"$GIT_URL\", \"branch\": \"main\", \"path\": \"\" }
    }
  }"

echo "registered provisioning repository $REPO_NAME -> $GIT_URL"
