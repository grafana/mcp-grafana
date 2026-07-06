#!/bin/sh
# Quickwit data seeding script for integration tests.
# Creates a test index and ingests sample log documents.

set -e

QW_URL="${QW_URL:-http://quickwit:7280/api/v1}"

format_ts() {
  offset="$1"
  epoch=$(($(date +%s) - offset))
  date -u -d "@${epoch}" +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -r "${epoch}" +"%Y-%m-%dT%H:%M:%SZ"
}

echo "Waiting for Quickwit to be ready..."
until curl -sf "${QW_URL}/version" > /dev/null 2>&1; do
  sleep 2
done
echo "Quickwit is ready."

INDEX_CONFIG='version: 0.8
index_id: test-logs
doc_mapping:
  mode: dynamic
  field_mappings:
    - name: timestamp
      type: datetime
      input_formats:
        - rfc3339
      output_format: rfc3339
      fast: true
    - name: body
      type: text
      tokenizer: default
      record: position
    - name: severity_text
      type: text
      tokenizer: raw
      fast: true
    - name: service_name
      type: text
      tokenizer: raw
      fast: true
  timestamp_field: timestamp
search_settings:
  default_search_fields: [severity_text, body]'

echo "Creating test-logs index..."
curl -sf -X POST "${QW_URL}/indexes" \
  -H 'Content-Type: application/yaml' \
  --data-binary "${INDEX_CONFIG}" || echo "Index may already exist, continuing."

echo "Ingesting sample log documents..."
# Bulk ingest in one request. Sequential commit=wait_for per document hangs up on tests
# A single request with commit=force adds all docs reliably.
curl -sf -X POST "${QW_URL}/test-logs/ingest?commit=force" \
  -H 'Content-Type: application/json' \
  --data-binary @- <<EOF
{"timestamp": "$(format_ts 60)", "body": "GET /api/users 200 OK", "severity_text": "INFO", "service_name": "api-gateway"}
{"timestamp": "$(format_ts 120)", "body": "POST /api/login 401 Unauthorized", "severity_text": "WARN", "service_name": "auth-service"}
{"timestamp": "$(format_ts 180)", "body": "Database connection timeout after 30s", "severity_text": "ERROR", "service_name": "user-service"}
{"timestamp": "$(format_ts 240)", "body": "GET /api/health 200 OK", "severity_text": "INFO", "service_name": "api-gateway"}
{"timestamp": "$(format_ts 300)", "body": "Cache miss for key user:1234", "severity_text": "DEBUG", "service_name": "cache-service"}
{"timestamp": "$(format_ts 360)", "body": "POST /api/orders 201 Created", "severity_text": "INFO", "service_name": "order-service"}
{"timestamp": "$(format_ts 420)", "body": "Failed to parse request body: invalid JSON", "severity_text": "ERROR", "service_name": "api-gateway"}
{"timestamp": "$(format_ts 480)", "body": "GET /api/products 200 OK", "severity_text": "INFO", "service_name": "product-service"}
{"timestamp": "$(format_ts 540)", "body": "Rate limit exceeded for IP 192.168.1.100", "severity_text": "WARN", "service_name": "api-gateway"}
{"timestamp": "$(format_ts 600)", "body": "Scheduled job completed: cleanup_sessions", "severity_text": "INFO", "service_name": "scheduler"}
EOF

echo ""
echo "Quickwit seeding complete."
