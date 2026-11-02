#!/bin/bash
set -e

echo "Seeding CloudWatch test data..."

# Create test metrics in Test/Application namespace
awslocal cloudwatch put-metric-data \
  --namespace "Test/Application" \
  --metric-name "CPUUtilization" \
  --dimensions "ServiceName=test-service" \
  --value 45.5 \
  --unit Percent

awslocal cloudwatch put-metric-data \
  --namespace "Test/Application" \
  --metric-name "MemoryUtilization" \
  --dimensions "ServiceName=test-service" \
  --value 1024 \
  --unit Megabytes

awslocal cloudwatch put-metric-data \
  --namespace "Test/Application" \
  --metric-name "RequestCount" \
  --dimensions "ServiceName=api-gateway" \
  --value 100 \
  --unit Count

# Create test metrics in AWS/EC2 namespace
awslocal cloudwatch put-metric-data \
  --namespace "AWS/EC2" \
  --metric-name "CPUUtilization" \
  --dimensions "InstanceId=i-12345678" \
  --value 25.0 \
  --unit Percent

awslocal cloudwatch put-metric-data \
  --namespace "AWS/EC2" \
  --metric-name "NetworkIn" \
  --dimensions "InstanceId=i-12345678" \
  --value 1000000 \
  --unit Bytes

# CloudWatch Logs test data
echo "Creating CloudWatch Logs test data..."

awslocal logs create-log-group \
  --log-group-name "test-application-logs" \
  --region us-east-1

awslocal logs create-log-stream \
  --log-group-name "test-application-logs" \
  --log-stream-name "test-stream-1" \
  --region us-east-1

TIMESTAMP=$(date +%s000)
awslocal logs put-log-events \
  --log-group-name "test-application-logs" \
  --log-stream-name "test-stream-1" \
  --region us-east-1 \
  --log-events \
    "[{\"timestamp\":${TIMESTAMP},\"message\":\"ERROR: Connection timeout in service handler\"}, \
      {\"timestamp\":$((TIMESTAMP+1000)),\"message\":\"INFO: Request processed successfully\"}, \
      {\"timestamp\":$((TIMESTAMP+2000)),\"message\":\"WARN: High memory usage detected\"}, \
      {\"timestamp\":$((TIMESTAMP+3000)),\"message\":\"ERROR: Database query failed\"}, \
      {\"timestamp\":$((TIMESTAMP+4000)),\"message\":\"INFO: Health check passed\"}]"

echo "CloudWatch test data seeded successfully"
