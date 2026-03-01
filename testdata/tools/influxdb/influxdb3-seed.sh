#!/bin/bash
echo "Seed script start"
sleep 5
AUTH_TOKEN="apiv3_OgXAgbMRgiGXcAQaFLJoaw=="

DB_NAME="system-logs"

echo "Creating database: $DB_NAME"


influxdb3 create database $DB_NAME --token $AUTH_TOKEN

# --- Generate Seed Data (Line Protocol) ---
TIMESTAMP=$(date +%s%N)
ONE_HOUR_AGO=$(($(date +%s%N) - 3600000000000))

echo "Seeding data..."
# Measurement 1: auth_events (Security Logs)
# Measurement 2: resource_usage (Hardware Logs)

cat <<EOF | influxdb3 write --database $DB_NAME --token $AUTH_TOKEN
auth_events,service=ssh,status=fail,ip=192.168.1.50 attempt_count=5,severity=3 $ONE_HOUR_AGO
auth_events,service=web,status=success,ip=10.0.0.15 attempt_count=1,severity=1 $TIMESTAMP
resource_usage,host=server-alpha,region=us-east cpu_util=45.2,mem_free_gb=12.5,load_1m=1.05 $ONE_HOUR_AGO
resource_usage,host=server-alpha,region=us-east cpu_util=88.9,mem_free_gb=2.1,load_1m=4.20 $TIMESTAMP
resource_usage,host=server-beta,region=us-west cpu_util=12.1,mem_free_gb=30.4,load_1m=0.05 $TIMESTAMP
EOF
