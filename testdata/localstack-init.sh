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

echo "CloudWatch test data seeded successfully"
