-- MySQL initialization script for integration tests
-- This script provides seed data for time-series, logs, and metrics across multiple databases

SET NAMES utf8mb4;
SET @now = NOW(3);
-- ---------------------------------------------------------
-- Database: shop_performance
-- ---------------------------------------------------------
CREATE DATABASE IF NOT EXISTS shop_performance;
USE shop_performance;

-- Metrics table for time-series data
CREATE TABLE IF NOT EXISTS api_metrics (
    id INT AUTO_INCREMENT PRIMARY KEY,
    timestamp DATETIME(3) NOT NULL,
    endpoint VARCHAR(255),
    status_code INT,
    latency_ms DOUBLE PRECISION,
    service_name VARCHAR(100),
    INDEX(timestamp),
    INDEX(service_name)
);

-- Insert metrics with varying times
INSERT INTO api_metrics (timestamp, endpoint, status_code, latency_ms, service_name) VALUES
    (@now, '/api/v1/products', 200, 45.2, 'inventory-service'),
    (DATE_SUB(@now, INTERVAL 1 MINUTE), '/api/v1/products', 200, 42.1, 'inventory-service'),
    (DATE_SUB(@now, INTERVAL 2 MINUTE), '/api/v1/products', 200, 48.5, 'inventory-service'),
    (DATE_SUB(@now, INTERVAL 3 MINUTE), '/api/v1/checkout', 500, 1200.0, 'payment-service'),
    (DATE_SUB(@now, INTERVAL 4 MINUTE), '/api/v1/checkout', 200, 250.3, 'payment-service'),
    (DATE_SUB(@now, INTERVAL 5 MINUTE), '/api/v1/cart', 200, 15.7, 'inventory-service'),
    (DATE_SUB(@now, INTERVAL 10 MINUTE), '/api/v1/products', 200, 39.9, 'inventory-service'),
    (DATE_SUB(@now, INTERVAL 15 MINUTE), '/api/v1/products', 200, 41.2, 'inventory-service'),
    (DATE_SUB(@now, INTERVAL 1 HOUR), '/api/v1/health', 200, 5.2, 'api-gateway'),
    (DATE_SUB(@now, INTERVAL 6 HOUR), '/api/v1/health', 200, 4.8, 'api-gateway'),
    (DATE_SUB(@now, INTERVAL 1 DAY), '/api/v1/report', 200, 5500.0, 'analytics-service');

-- ---------------------------------------------------------
-- Database: infrastructure_logs
-- ---------------------------------------------------------
CREATE DATABASE IF NOT EXISTS infrastructure_logs;
USE infrastructure_logs;

-- Logs table similar to OpenTelemetry schema
CREATE TABLE IF NOT EXISTS logs (
    id INT AUTO_INCREMENT PRIMARY KEY,
    timestamp DATETIME(3) NOT NULL,
    body TEXT,
    service_name VARCHAR(100),
    severity_text VARCHAR(20),
    trace_id VARCHAR(64),
    INDEX(timestamp),
    INDEX(severity_text)
);

-- Insert logs with varying times
INSERT INTO logs (timestamp, body, service_name, severity_text, trace_id) VALUES
    (@now, 'Order processed successfully', 'order-service', 'INFO', '5938475abcde9876'),
    (DATE_SUB(@now, INTERVAL 30 SECOND), 'Connection pool saturated', 'db-proxy', 'WARN', NULL),
    (DATE_SUB(@now, INTERVAL 2 MINUTE), 'Payment authorization failed', 'payment-service', 'ERROR', '12345678abcdef00'),
    (DATE_SUB(@now, INTERVAL 5 MINUTE), 'Cache miss for product: 123', 'inventory-service', 'DEBUG', NULL),
    (DATE_SUB(@now, INTERVAL 15 MINUTE), 'Service started', 'order-service', 'INFO', NULL),
    (DATE_SUB(@now, INTERVAL 1 HOUR), 'Garbage collection completed (250ms)', 'inventory-service', 'INFO', NULL),
    (DATE_SUB(@now, INTERVAL 12 HOUR), 'Unexpected EOF reading from socket', 'api-gateway', 'ERROR', 'a0b1c2d3e4f56789'),
    (DATE_SUB(@now, INTERVAL 2 DAY), 'System reboot following maintenance', 'host-01', 'INFO', NULL);

-- Host metrics table
CREATE TABLE IF NOT EXISTS host_metrics (
    id INT AUTO_INCREMENT PRIMARY KEY,
    timestamp DATETIME(3) NOT NULL,
    host VARCHAR(100),
    cpu_usage DOUBLE PRECISION,
    mem_usage DOUBLE PRECISION,
    INDEX(timestamp),
    INDEX(host)
);

-- Insert host metrics
INSERT INTO host_metrics (timestamp, host, cpu_usage, mem_usage) VALUES
    (@now, 'server-01', 12.5, 60.2),
    (DATE_SUB(@now, INTERVAL 1 MINUTE), 'server-01', 15.1, 60.5),
    (DATE_SUB(@now, INTERVAL 2 MINUTE), 'server-01', 14.8, 60.4),
    (@now, 'server-02', 85.2, 92.1),
    (DATE_SUB(@now, INTERVAL 1 MINUTE), 'server-02', 82.4, 91.8),
    (DATE_SUB(@now, INTERVAL 2 MINUTE), 'server-02', 88.7, 92.5);

CREATE USER 'grafana'@'%' IDENTIFIED WITH mysql_native_password BY 'password';
GRANT SELECT ON infrastructure_logs.* TO 'grafana'@'%';