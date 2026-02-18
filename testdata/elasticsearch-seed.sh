#!/bin/bash
# Elasticsearch data seeding script for integration tests.
# This script waits for Elasticsearch to be ready, then indexes sample log data.

set -e

ES_URL="${ES_URL:-http://elasticsearch:9200}"

echo "Waiting for Elasticsearch to be ready..."
until curl -sf "${ES_URL}/_cluster/health" > /dev/null 2>&1; do
  sleep 2
done
echo "Elasticsearch is ready."

# Create an index template for test logs
curl -sf -X PUT "${ES_URL}/_index_template/test-logs-template" \
  -H 'Content-Type: application/json' \
  -d '{
  "index_patterns": ["test-logs-*"],
  "template": {
    "settings": {
      "number_of_shards": 1,
      "number_of_replicas": 0
    },
    "mappings": {
      "properties": {
        "@timestamp": { "type": "date" },
        "message": { "type": "text" },
        "level": { "type": "keyword" },
        "service": { "type": "keyword" },
        "host": { "type": "keyword" },
        "status_code": { "type": "integer" },
        "duration_ms": { "type": "float" },
        "trace_id": { "type": "keyword" }
      }
    }
  }
}'
echo ""
echo "Created index template."

# Get current timestamp in milliseconds for realistic data
NOW_MS=$(date +%s000)
# Offsets in milliseconds (going back from now)
O1=$((NOW_MS - 60000))
O2=$((NOW_MS - 120000))
O3=$((NOW_MS - 180000))
O4=$((NOW_MS - 240000))
O5=$((NOW_MS - 300000))
O6=$((NOW_MS - 360000))
O7=$((NOW_MS - 420000))
O8=$((NOW_MS - 480000))
O9=$((NOW_MS - 540000))
O10=$((NOW_MS - 600000))

# Bulk index sample log documents
curl -sf -X POST "${ES_URL}/test-logs-2024/_bulk" \
  -H 'Content-Type: application/x-ndjson' \
  -d '{"index":{}}
{"@timestamp":'"${O1}"',"message":"GET /api/users 200 OK","level":"info","service":"api-gateway","host":"server1","status_code":200,"duration_ms":12.5,"trace_id":"abc123"}
{"index":{}}
{"@timestamp":'"${O2}"',"message":"POST /api/login 401 Unauthorized","level":"warn","service":"auth-service","host":"server2","status_code":401,"duration_ms":45.2,"trace_id":"def456"}
{"index":{}}
{"@timestamp":'"${O3}"',"message":"Database connection timeout after 30s","level":"error","service":"user-service","host":"server1","status_code":500,"duration_ms":30000.0,"trace_id":"ghi789"}
{"index":{}}
{"@timestamp":'"${O4}"',"message":"GET /api/health 200 OK","level":"info","service":"api-gateway","host":"server1","status_code":200,"duration_ms":1.2,"trace_id":"jkl012"}
{"index":{}}
{"@timestamp":'"${O5}"',"message":"Cache miss for key user:1234","level":"debug","service":"cache-service","host":"server3","status_code":200,"duration_ms":0.5,"trace_id":"mno345"}
{"index":{}}
{"@timestamp":'"${O6}"',"message":"POST /api/orders 201 Created","level":"info","service":"order-service","host":"server2","status_code":201,"duration_ms":89.3,"trace_id":"pqr678"}
{"index":{}}
{"@timestamp":'"${O7}"',"message":"Failed to parse request body: invalid JSON","level":"error","service":"api-gateway","host":"server1","status_code":400,"duration_ms":2.1,"trace_id":"stu901"}
{"index":{}}
{"@timestamp":'"${O8}"',"message":"GET /api/products 200 OK","level":"info","service":"product-service","host":"server3","status_code":200,"duration_ms":23.7,"trace_id":"vwx234"}
{"index":{}}
{"@timestamp":'"${O9}"',"message":"Rate limit exceeded for IP 192.168.1.100","level":"warn","service":"api-gateway","host":"server1","status_code":429,"duration_ms":0.8,"trace_id":"yza567"}
{"index":{}}
{"@timestamp":'"${O10}"',"message":"Scheduled job completed: cleanup_sessions","level":"info","service":"scheduler","host":"server2","status_code":200,"duration_ms":1523.4,"trace_id":"bcd890"}
'
echo ""
echo "Indexed sample log data."

# Refresh the index to make documents searchable immediately
curl -sf -X POST "${ES_URL}/test-logs-2024/_refresh"
echo ""
echo "Elasticsearch seeding complete."
