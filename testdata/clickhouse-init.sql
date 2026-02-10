-- ClickHouse initialization script for integration tests
-- This script is executed when the ClickHouse container starts

CREATE DATABASE IF NOT EXISTS test;

-- Create a logs table similar to OpenTelemetry logs schema
CREATE TABLE IF NOT EXISTS test.logs (
    Timestamp DateTime64(3),
    Body String,
    ServiceName String,
    SeverityText String
) ENGINE = MergeTree() ORDER BY Timestamp;

-- Insert test data
INSERT INTO test.logs VALUES
    (now(), 'Test log entry 1', 'test-service', 'INFO'),
    (now(), 'Test error message', 'test-service', 'ERROR'),
    (now(), 'Another log entry', 'api-gateway', 'DEBUG'),
    (now(), 'Warning from service', 'test-service', 'WARN'),
    (now(), 'Debug information', 'api-gateway', 'DEBUG');

-- Create a metrics table for additional testing
CREATE TABLE IF NOT EXISTS test.metrics (
    Timestamp DateTime64(3),
    MetricName String,
    Value Float64,
    ServiceName String
) ENGINE = MergeTree() ORDER BY Timestamp;

-- Insert test metrics
INSERT INTO test.metrics VALUES
    (now(), 'cpu_usage', 45.5, 'test-service'),
    (now(), 'memory_usage', 1024.0, 'test-service'),
    (now(), 'request_count', 100, 'api-gateway'),
    (now(), 'error_rate', 0.05, 'test-service');
