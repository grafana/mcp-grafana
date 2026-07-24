-- PostgreSQL initialization script for integration tests
-- Seed data for time-series, logs, and metrics across multiple schemas

---------------------------------------------------------
-- Database: infrastructure_logs (Current)
---------------------------------------------------------

-- Schema: public (Default)
CREATE TABLE IF NOT EXISTS api_metrics (
    id SERIAL PRIMARY KEY,
    timestamp TIMESTAMP(3) NOT NULL,
    endpoint VARCHAR(255),
    status_code INT,
    latency_ms DOUBLE PRECISION,
    service_name VARCHAR(100)
);

CREATE INDEX IF NOT EXISTS idx_api_metrics_timestamp ON api_metrics(timestamp);
CREATE INDEX IF NOT EXISTS idx_api_metrics_service_name ON api_metrics(service_name);

INSERT INTO api_metrics (timestamp, endpoint, status_code, latency_ms, service_name) VALUES
    (now(), '/api/v1/products', 200, 45.2, 'inventory-service'),
    (now() - interval '1 minute', '/api/v1/products', 200, 42.1, 'inventory-service'),
    (now() - interval '2 minutes', '/api/v1/products', 200, 48.5, 'inventory-service'),
    (now() - interval '3 minutes', '/api/v1/checkout', 500, 1200.0, 'payment-service'),
    (now() - interval '4 minutes', '/api/v1/checkout', 200, 250.3, 'payment-service'),
    (now() - interval '5 minutes', '/api/v1/cart', 200, 15.7, 'inventory-service'),
    (now() - interval '10 minutes', '/api/v1/products', 200, 39.9, 'inventory-service'),
    (now() - interval '15 minutes', '/api/v1/products', 200, 41.2, 'inventory-service'),
    (now() - interval '1 hour', '/api/v1/health', 200, 5.2, 'api-gateway'),
    (now() - interval '6 hours', '/api/v1/health', 200, 4.8, 'api-gateway'),
    (now() - interval '1 day', '/api/v1/report', 200, 5500.0, 'analytics-service');

CREATE TABLE IF NOT EXISTS logs (
    id SERIAL PRIMARY KEY,
    timestamp TIMESTAMP(3) NOT NULL,
    body TEXT,
    service_name VARCHAR(100),
    severity_text VARCHAR(20),
    trace_id VARCHAR(64)
);

CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);
CREATE INDEX IF NOT EXISTS idx_logs_severity ON logs(severity_text);

INSERT INTO logs (timestamp, body, service_name, severity_text, trace_id) VALUES
    (now(), 'Order processed successfully', 'order-service', 'INFO', '5938475abcde9876'),
    (now() - interval '30 seconds', 'Connection pool saturated', 'db-proxy', 'WARN', NULL),
    (now() - interval '2 minutes', 'Payment authorization failed', 'payment-service', 'ERROR', '12345678abcdef00'),
    (now() - interval '5 minutes', 'Cache miss for product: 123', 'inventory-service', 'DEBUG', NULL),
    (now() - interval '15 minutes', 'Service started', 'order-service', 'INFO', NULL),
    (now() - interval '1 hour', 'Garbage collection completed (250ms)', 'inventory-service', 'INFO', NULL),
    (now() - interval '12 hours', 'Unexpected EOF reading from socket', 'api-gateway', 'ERROR', 'a0b1c2d3e4f56789'),
    (now() - interval '2 days', 'System reboot following maintenance', 'host-01', 'INFO', NULL);

CREATE TABLE IF NOT EXISTS host_metrics (
    id SERIAL PRIMARY KEY,
    timestamp TIMESTAMP(3) NOT NULL,
    host VARCHAR(100),
    cpu_usage DOUBLE PRECISION,
    mem_usage DOUBLE PRECISION
);

CREATE INDEX IF NOT EXISTS idx_host_metrics_timestamp ON host_metrics(timestamp);
CREATE INDEX IF NOT EXISTS idx_host_metrics_host ON host_metrics(host);

INSERT INTO host_metrics (timestamp, host, cpu_usage, mem_usage) VALUES
    (now(), 'server-01', 12.5, 60.2),
    (now() - interval '1 minute', 'server-01', 15.1, 60.5),
    (now() - interval '2 minutes', 'server-01', 14.8, 60.4),
    (now(), 'server-02', 85.2, 92.1),
    (now() - interval '1 minute', 'server-02', 82.4, 91.8),
    (now() - interval '2 minutes', 'server-02', 88.7, 92.5);

---------------------------------------------------------
-- Schema: reporting
---------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS reporting;

CREATE TABLE IF NOT EXISTS reporting.sales (
    id SERIAL PRIMARY KEY,
    timestamp TIMESTAMP(3) NOT NULL,
    product_name VARCHAR(255),
    amount DECIMAL(18,2)
);

INSERT INTO reporting.sales (timestamp, product_name, amount) VALUES
    (now(), 'Product A', 100.00),
    (now() - interval '1 day', 'Product B', 150.50),
    (now() - interval '2 days', 'Product C', 200.75);

---------------------------------------------------------
-- Schema: catalog
---------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS catalog;

CREATE TABLE IF NOT EXISTS catalog.products (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) UNIQUE NOT NULL,
    category VARCHAR(100),
    price DECIMAL(10,2),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO catalog.products (name, category, price) VALUES
    ('Product A', 'Tools', 49.99),
    ('Product B', 'Electronics', 199.50),
    ('Product C', 'Parts', 12.00);

---------------------------------------------------------
-- Schema: events
---------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS events;

CREATE TABLE IF NOT EXISTS events.audit_log (
    id SERIAL PRIMARY KEY,
    occurred_at TIMESTAMP NOT NULL DEFAULT now(),
    actor VARCHAR(100),
    action VARCHAR(50),
    target_type VARCHAR(50),
    target_id VARCHAR(100),
    metadata JSONB,
    tags TEXT[]
);

INSERT INTO events.audit_log (actor, action, target_type, target_id, metadata, tags) VALUES
    ('admin', 'login', 'user', '1', '{"ip": "192.168.1.1"}', '{"security", "auth"}'),
    ('system', 'backup_started', 'database', 'infrastructure_logs', '{"size_gb": 1.2}', '{"maintenance", "backup"}'),
    ('user_test', 'update_profile', 'user', '42', '{"fields": ["email", "phone"]}', '{"user-action"}');

---------------------------------------------------------
-- Configuration & Permissions (infrastructure_logs)
---------------------------------------------------------

-- Create user for Grafana if it doesn't exist
DO
$do$
BEGIN
   IF NOT EXISTS (
      SELECT FROM pg_catalog.pg_roles
      WHERE  rolname = 'grafana') THEN

      CREATE ROLE grafana LOGIN PASSWORD 'password';
   END IF;
END
$do$;

-- Grant access to all schemas/tables in current DB
DO
$do$
DECLARE
    schema_name_ text;
BEGIN
    FOR schema_name_ IN SELECT schema_name FROM information_schema.schemata 
                       WHERE schema_name NOT IN ('information_schema', 'pg_catalog')
    LOOP
        EXECUTE format('GRANT USAGE ON SCHEMA %I TO grafana', schema_name_);
        EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA %I TO grafana', schema_name_);
        EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA %I GRANT SELECT ON TABLES TO grafana', schema_name_);
    END LOOP;
END
$do$;

---------------------------------------------------------
-- NEW DATABASE: application_db
---------------------------------------------------------
CREATE DATABASE application_db;
\c application_db

---------------------------------------------------------
-- Schema: production
---------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS production;

CREATE TABLE IF NOT EXISTS production.orders (
    id SERIAL PRIMARY KEY,
    customer_id INT NOT NULL,
    order_date TIMESTAMP DEFAULT now(),
    total_amount DECIMAL(10,2),
    status VARCHAR(20)
);

CREATE TABLE IF NOT EXISTS production.inventory (
    id SERIAL PRIMARY KEY,
    sku VARCHAR(50) UNIQUE,
    quantity INT DEFAULT 0,
    last_updated TIMESTAMP DEFAULT now()
);

INSERT INTO production.orders (customer_id, total_amount, status) VALUES
    (101, 299.99, 'completed'),
    (102, 45.50, 'pending');

INSERT INTO production.inventory (sku, quantity) VALUES
    ('PROD-001', 500),
    ('PROD-002', 125);

---------------------------------------------------------
-- Schema: staging
---------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS staging;

CREATE TABLE IF NOT EXISTS staging.beta_features (
    id SERIAL PRIMARY KEY,
    feature_name VARCHAR(100),
    is_enabled BOOLEAN DEFAULT false,
    pilot_users INT[]
);

CREATE TABLE IF NOT EXISTS staging.test_results (
    id SERIAL PRIMARY KEY,
    test_run_id VARCHAR(50),
    passed BOOLEAN,
    duration_ms INT
);

INSERT INTO staging.beta_features (feature_name, is_enabled, pilot_users) VALUES
    ('dark_mode_v2', true, '{1, 2, 3}'),
    ('advanced_search', false, '{}');

INSERT INTO staging.test_results (test_run_id, passed, duration_ms) VALUES
    ('RUN_001', true, 1200),
    ('RUN_002', false, 850);

---------------------------------------------------------
-- Permissions (application_db)
---------------------------------------------------------

-- Note: Role 'grafana' already exists globally, but we need to grant access in this new DB
DO
$do$
DECLARE
    schema_name_ text;
BEGIN
    FOR schema_name_ IN SELECT schema_name FROM information_schema.schemata 
                       WHERE schema_name NOT IN ('information_schema', 'pg_catalog')
    LOOP
        EXECUTE format('GRANT USAGE ON SCHEMA %I TO grafana', schema_name_);
        EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA %I TO grafana', schema_name_);
        EXECUTE format('ALTER DEFAULT PRIVILEGES IN SCHEMA %I GRANT SELECT ON TABLES TO grafana', schema_name_);
    END LOOP;
END
$do$;
