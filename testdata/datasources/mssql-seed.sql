-- Microsoft SQL Server initialization script for integration tests
-- Seed data for time-series, logs, and metrics across multiple databases

SET NOCOUNT ON;
GO

---------------------------------------------------------
-- Database: shop_performance
---------------------------------------------------------
IF DB_ID('shop_performance') IS NULL
    CREATE DATABASE shop_performance;
GO

USE shop_performance;
GO

-- Metrics table for time-series data
IF OBJECT_ID('api_metrics', 'U') IS NULL
CREATE TABLE api_metrics (
    id INT IDENTITY(1,1) PRIMARY KEY,
    [timestamp] DATETIME2(3) NOT NULL,
    endpoint NVARCHAR(255),
    status_code INT,
    latency_ms FLOAT,
    service_name NVARCHAR(100)
);

IF NOT EXISTS (SELECT * FROM sys.indexes WHERE name = 'idx_api_metrics_timestamp' AND object_id = OBJECT_ID('api_metrics'))
CREATE INDEX idx_api_metrics_timestamp ON api_metrics([timestamp]);

IF NOT EXISTS (SELECT * FROM sys.indexes WHERE name = 'idx_api_metrics_service_name' AND object_id = OBJECT_ID('api_metrics'))
CREATE INDEX idx_api_metrics_service_name ON api_metrics(service_name);
GO

-- Insert metrics with varying times
DECLARE @now DATETIME2(3) = SYSDATETIME();

INSERT INTO api_metrics ([timestamp], endpoint, status_code, latency_ms, service_name) VALUES
    (@now, '/api/v1/products', 200, 45.2, 'inventory-service'),
    (DATEADD(MINUTE, -1, @now), '/api/v1/products', 200, 42.1, 'inventory-service'),
    (DATEADD(MINUTE, -2, @now), '/api/v1/products', 200, 48.5, 'inventory-service'),
    (DATEADD(MINUTE, -3, @now), '/api/v1/checkout', 500, 1200.0, 'payment-service'),
    (DATEADD(MINUTE, -4, @now), '/api/v1/checkout', 200, 250.3, 'payment-service'),
    (DATEADD(MINUTE, -5, @now), '/api/v1/cart', 200, 15.7, 'inventory-service'),
    (DATEADD(MINUTE, -10, @now), '/api/v1/products', 200, 39.9, 'inventory-service'),
    (DATEADD(MINUTE, -15, @now), '/api/v1/products', 200, 41.2, 'inventory-service'),
    (DATEADD(HOUR, -1, @now), '/api/v1/health', 200, 5.2, 'api-gateway'),
    (DATEADD(HOUR, -6, @now), '/api/v1/health', 200, 4.8, 'api-gateway'),
    (DATEADD(DAY, -1, @now), '/api/v1/report', 200, 5500.0, 'analytics-service');
GO

---------------------------------------------------------
-- Schema: reporting
---------------------------------------------------------
IF NOT EXISTS (SELECT * FROM sys.schemas WHERE name = 'reporting')
BEGIN
    EXEC('CREATE SCHEMA reporting')
END
GO

IF OBJECT_ID('reporting.sales', 'U') IS NULL
CREATE TABLE reporting.sales (
    id INT IDENTITY(1,1) PRIMARY KEY,
    [timestamp] DATETIME2(3) NOT NULL,
    product_name NVARCHAR(255),
    amount DECIMAL(18,2)
);

INSERT INTO reporting.sales ([timestamp], product_name, amount) VALUES
    (SYSDATETIME(), 'Product A', 100.00),
    (DATEADD(DAY, -1, SYSDATETIME()), 'Product B', 150.50);
GO

---------------------------------------------------------
-- Database: infrastructure_logs
---------------------------------------------------------
IF DB_ID('infrastructure_logs') IS NULL
    CREATE DATABASE infrastructure_logs;
GO

USE infrastructure_logs;
GO

-- Logs table similar to OpenTelemetry schema
IF OBJECT_ID('logs', 'U') IS NULL
CREATE TABLE logs (
    id INT IDENTITY(1,1) PRIMARY KEY,
    [timestamp] DATETIME2(3) NOT NULL,
    body NVARCHAR(MAX),
    service_name NVARCHAR(100),
    severity_text NVARCHAR(20),
    trace_id NVARCHAR(64)
);

IF NOT EXISTS (SELECT * FROM sys.indexes WHERE name = 'idx_logs_timestamp' AND object_id = OBJECT_ID('logs'))
CREATE INDEX idx_logs_timestamp ON logs([timestamp]);

IF NOT EXISTS (SELECT * FROM sys.indexes WHERE name = 'idx_logs_severity' AND object_id = OBJECT_ID('logs'))
CREATE INDEX idx_logs_severity ON logs(severity_text);
GO

-- Insert logs
DECLARE @now DATETIME2(3) = SYSDATETIME();

INSERT INTO logs ([timestamp], body, service_name, severity_text, trace_id) VALUES
    (@now, 'Order processed successfully', 'order-service', 'INFO', '5938475abcde9876'),
    (DATEADD(SECOND, -30, @now), 'Connection pool saturated', 'db-proxy', 'WARN', NULL),
    (DATEADD(MINUTE, -2, @now), 'Payment authorization failed', 'payment-service', 'ERROR', '12345678abcdef00'),
    (DATEADD(MINUTE, -5, @now), 'Cache miss for product: 123', 'inventory-service', 'DEBUG', NULL),
    (DATEADD(MINUTE, -15, @now), 'Service started', 'order-service', 'INFO', NULL),
    (DATEADD(HOUR, -1, @now), 'Garbage collection completed (250ms)', 'inventory-service', 'INFO', NULL),
    (DATEADD(HOUR, -12, @now), 'Unexpected EOF reading from socket', 'api-gateway', 'ERROR', 'a0b1c2d3e4f56789'),
    (DATEADD(DAY, -2, @now), 'System reboot following maintenance', 'host-01', 'INFO', NULL);
GO

---------------------------------------------------------
-- Host metrics table
---------------------------------------------------------
IF OBJECT_ID('host_metrics', 'U') IS NULL
CREATE TABLE host_metrics (
    id INT IDENTITY(1,1) PRIMARY KEY,
    [timestamp] DATETIME2(3) NOT NULL,
    host NVARCHAR(100),
    cpu_usage FLOAT,
    mem_usage FLOAT
);

IF NOT EXISTS (SELECT * FROM sys.indexes WHERE name = 'idx_host_metrics_timestamp' AND object_id = OBJECT_ID('host_metrics'))
CREATE INDEX idx_host_metrics_timestamp ON host_metrics([timestamp]);

IF NOT EXISTS (SELECT * FROM sys.indexes WHERE name = 'idx_host_metrics_host' AND object_id = OBJECT_ID('host_metrics'))
CREATE INDEX idx_host_metrics_host ON host_metrics(host);
GO

DECLARE @now DATETIME2(3) = SYSDATETIME();

INSERT INTO host_metrics ([timestamp], host, cpu_usage, mem_usage) VALUES
    (@now, 'server-01', 12.5, 60.2),
    (DATEADD(MINUTE, -1, @now), 'server-01', 15.1, 60.5),
    (DATEADD(MINUTE, -2, @now), 'server-01', 14.8, 60.4),
    (@now, 'server-02', 85.2, 92.1),
    (DATEADD(MINUTE, -1, @now), 'server-02', 82.4, 91.8),
    (DATEADD(MINUTE, -2, @now), 'server-02', 88.7, 92.5);
GO

---------------------------------------------------------
-- Create user for Grafana
---------------------------------------------------------
USE master;
GO

IF NOT EXISTS (SELECT * FROM sys.server_principals WHERE name = 'grafana')
CREATE LOGIN grafana WITH PASSWORD = 'T3st@SqlServer';
GO

USE infrastructure_logs;
GO

IF NOT EXISTS (SELECT * FROM sys.database_principals WHERE name = 'grafana')
CREATE USER grafana FOR LOGIN grafana;
GO

GRANT SELECT TO grafana;
GO

USE shop_performance;
GO

IF NOT EXISTS (SELECT * FROM sys.database_principals WHERE name = 'grafana')
CREATE USER grafana FOR LOGIN grafana;
GO

GRANT SELECT TO grafana;
GO