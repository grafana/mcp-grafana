CREATE DATABASE IF NOT EXISTS test;
USE test;

CREATE TABLE IF NOT EXISTS logs (
    id INT AUTO_INCREMENT PRIMARY KEY,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    body TEXT,
    service_name VARCHAR(255),
    severity_text VARCHAR(50)
);

INSERT INTO logs (body, service_name, severity_text) VALUES
    ('Test log entry 1', 'test-service', 'INFO'),
    ('Test error message', 'test-service', 'ERROR'),
    ('Another log entry', 'api-gateway', 'DEBUG'),
    ('Warning from service', 'test-service', 'WARN'),
    ('Debug information', 'api-gateway', 'DEBUG');

CREATE TABLE IF NOT EXISTS metrics (
    id INT AUTO_INCREMENT PRIMARY KEY,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    metric_name VARCHAR(255),
    value DOUBLE,
    service_name VARCHAR(255)
);

INSERT INTO metrics (metric_name, value, service_name) VALUES
    ('cpu_usage', 45.5, 'test-service'),
    ('memory_usage', 1024.0, 'test-service'),
    ('request_count', 100, 'api-gateway'),
    ('error_rate', 0.05, 'test-service');
