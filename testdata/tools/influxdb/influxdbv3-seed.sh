#!/bin/bash

sleep 5

AUTH_TOKEN="apiv3_OgXAgbMRgiGXcAQaFLJoaw=="
DB_NAME="system-logs"

echo "Creating database: $DB_NAME"
influxdb3 create database $DB_NAME --token $AUTH_TOKEN

# --- Generate Timestamps ---
NOW=$(date +%s%N)
M1=$((NOW - 7200000000000))   # 2 hours ago
M2=$((NOW - 3600000000000))   # 1 hour ago
M3=$((NOW - 1800000000000))   # 30 min ago
M4=$((NOW - 900000000000))    # 15 min ago
M5=$((NOW - 300000000000))    # 5 min ago

echo "Seeding data..."

cat <<EOF | influxdb3 write --database $DB_NAME --token $AUTH_TOKEN
auth_events,service=ssh,status=fail,ip=192.168.1.50 attempt_count=5,severity=3 $M1
auth_events,service=ssh,status=fail,ip=192.168.1.51 attempt_count=3,severity=3 $M2
auth_events,service=ssh,status=fail,ip=10.0.0.99 attempt_count=8,severity=4 $M3
auth_events,service=web,status=success,ip=10.0.0.15 attempt_count=1,severity=1 $M4
auth_events,service=web,status=success,ip=172.16.0.5 attempt_count=1,severity=1 $M5
auth_events,service=vpn,status=fail,ip=203.0.113.42 attempt_count=12,severity=5 $M2
auth_events,service=vpn,status=success,ip=10.10.0.3 attempt_count=1,severity=1 $M3
auth_events,service=ftp,status=fail,ip=198.51.100.7 attempt_count=2,severity=2 $M1
auth_events,service=smtp,status=success,ip=10.0.1.20 attempt_count=1,severity=1 $M4
auth_events,service=rdp,status=fail,ip=203.0.113.10 attempt_count=20,severity=5 $M5
resource_usage,host=server-alpha,region=us-east cpu_util=45.2,mem_free_gb=12.5,disk_used_pct=55.0,load_1m=1.05,net_in_mbps=10.2 $M1
resource_usage,host=server-alpha,region=us-east cpu_util=67.8,mem_free_gb=8.3,disk_used_pct=55.3,load_1m=2.80,net_in_mbps=45.6 $M2
resource_usage,host=server-alpha,region=us-east cpu_util=88.9,mem_free_gb=2.1,disk_used_pct=55.5,load_1m=4.20,net_in_mbps=92.1 $M3
resource_usage,host=server-alpha,region=us-east cpu_util=91.2,mem_free_gb=1.5,disk_used_pct=55.7,load_1m=5.10,net_in_mbps=98.4 $M4
resource_usage,host=server-alpha,region=us-east cpu_util=75.4,mem_free_gb=4.2,disk_used_pct=55.8,load_1m=3.30,net_in_mbps=60.3 $M5
resource_usage,host=server-beta,region=us-west cpu_util=12.1,mem_free_gb=30.4,disk_used_pct=22.0,load_1m=0.05,net_in_mbps=5.1 $M1
resource_usage,host=server-beta,region=us-west cpu_util=18.5,mem_free_gb=28.9,disk_used_pct=22.1,load_1m=0.45,net_in_mbps=8.3 $M2
resource_usage,host=server-beta,region=us-west cpu_util=35.7,mem_free_gb=25.1,disk_used_pct=22.2,load_1m=1.20,net_in_mbps=20.7 $M3
resource_usage,host=server-gamma,region=eu-west cpu_util=55.3,mem_free_gb=16.0,disk_used_pct=70.1,load_1m=2.10,net_in_mbps=30.5 $M1
resource_usage,host=server-gamma,region=eu-west cpu_util=60.1,mem_free_gb=14.5,disk_used_pct=70.5,load_1m=2.50,net_in_mbps=35.9 $M2
resource_usage,host=server-gamma,region=eu-west cpu_util=72.4,mem_free_gb=10.2,disk_used_pct=71.0,load_1m=3.10,net_in_mbps=50.4 $M3
resource_usage,host=server-delta,region=ap-south cpu_util=5.2,mem_free_gb=60.1,disk_used_pct=10.0,load_1m=0.02,net_in_mbps=1.2 $M4
resource_usage,host=server-delta,region=ap-south cpu_util=8.9,mem_free_gb=58.4,disk_used_pct=10.1,load_1m=0.10,net_in_mbps=3.4 $M5
syslog,host=server-alpha,level=ERROR,facility=kern pid=4821 $M3
syslog,host=server-alpha,level=WARN,facility=kern pid=1 $M2
syslog,host=server-beta,level=INFO,facility=sshd pid=9201 $M1
syslog,host=server-gamma,level=ERROR,facility=nginx pid=3310 $M4
syslog,host=server-delta,level=INFO,facility=cron pid=7741 $M5
syslog,host=server-alpha,level=CRIT,facility=disk pid=1 $M5
http_requests,app=api-gateway,method=GET,status=200 count=1500,latency_ms=45.2,error_rate=0.0 $M1
http_requests,app=api-gateway,method=POST,status=201 count=320,latency_ms=120.5,error_rate=0.0 $M1
http_requests,app=api-gateway,method=GET,status=500 count=12,latency_ms=5001.0,error_rate=1.0 $M2
http_requests,app=api-gateway,method=GET,status=200 count=1800,latency_ms=42.1,error_rate=0.0 $M2
http_requests,app=checkout-svc,method=POST,status=200 count=200,latency_ms=350.0,error_rate=0.0 $M3
http_requests,app=checkout-svc,method=POST,status=500 count=25,latency_ms=6000.0,error_rate=1.0 $M3
http_requests,app=checkout-svc,method=POST,status=200 count=190,latency_ms=400.0,error_rate=0.0 $M4
db_queries,app=user-svc,db=postgres,op=SELECT duration_ms=5.2,rows_returned=10 $M1
db_queries,app=user-svc,db=postgres,op=INSERT duration_ms=12.1,rows_returned=1 $M2
db_queries,app=user-svc,db=redis,op=GET duration_ms=0.8,rows_returned=1 $M3
db_queries,app=checkout-svc,db=postgres,op=SELECT duration_ms=250.0,rows_returned=500 $M4
db_queries,app=checkout-svc,db=postgres,op=UPDATE duration_ms=80.5,rows_returned=1 $M5
queue_stats,app=worker,queue=email pending=45,processed=1200,failed=3,dlq_size=3 $M1
queue_stats,app=worker,queue=email pending=120,processed=1350,failed=5,dlq_size=8 $M2
queue_stats,app=worker,queue=sms pending=10,processed=980,failed=0,dlq_size=0 $M3
queue_stats,app=worker,queue=notifications pending=500,processed=4000,failed=12,dlq_size=12 $M4
EOF

echo "✅ Done! Seeded all data into: $DB_NAME"