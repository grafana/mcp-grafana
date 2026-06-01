#!/bin/sh
set -e

REPO_ROOT=/srv/git
SEED_DIR=/srv/seed
REPO_NAME=test-repo.git

mkdir -p "$REPO_ROOT"

if [ ! -d "$REPO_ROOT/$REPO_NAME" ]; then
  TMP=$(mktemp -d)

  cd "$TMP"
  git init -b main >/dev/null
  git config user.email integration-test@grafana.local
  git config user.name "Integration Test"

  # main branch: contents of seed/main
  cp -r "$SEED_DIR"/main/. .
  git add .
  git commit -m "initial commit" >/dev/null

  # feature branch: add the files from seed/extra on top of main
  git checkout -b feature/extra-dashboard >/dev/null
  cp -r "$SEED_DIR"/extra/. .
  git add .
  git diff --cached --quiet || git commit -m "add extra dashboard" >/dev/null
  git checkout main >/dev/null

  cd /
  git clone --bare "$TMP" "$REPO_ROOT/$REPO_NAME" >/dev/null 2>&1
  cd "$REPO_ROOT/$REPO_NAME"
  git config uploadpack.allowFilter true
  git config uploadpack.allowAnySHA1InWant true
  git update-server-info
  cd /
  rm -rf "$TMP"

  # Apache runs as `daemon`; it needs to read (and git-http-backend needs to
  # write pack indexes/temp files on first fetch).
  chown -R daemon:daemon "$REPO_ROOT"
fi

exec httpd -DFOREGROUND
