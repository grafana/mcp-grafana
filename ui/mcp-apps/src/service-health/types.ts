export type ServiceHealthTimeRange = '1h' | '6h' | '24h' | '7d';

export type ServiceHealthStatus = 'healthy' | 'degraded' | 'unknown';

export type ServiceHealthMetricUnit = 'ms' | 'ratio' | 'requests_per_second';

export interface ServiceHealthPoint {
  timestampMs: number;
  value: number | null;
}

export interface ServiceHealthMetric {
  value: number | null;
  unit: ServiceHealthMetricUnit;
  points: ServiceHealthPoint[];
  unavailableReason: string | null;
}

export interface ServiceHealthMetrics {
  latencyP95: ServiceHealthMetric;
  errorRatio: ServiceHealthMetric;
  requestRate: ServiceHealthMetric;
}

export interface ServiceHealthDependency {
  name: string;
  namespace: string | null;
  type: 'service' | 'database' | 'unknown';
  latencyP95Ms: number | null;
  errorRatio: number | null;
  unavailableReason: string | null;
}

export interface RenderServiceHealthResult {
  serviceName: string;
  serviceNamespace: string | null;
  datasourceUid: string;
  timeRange: ServiceHealthTimeRange;
  fromMs: number;
  toMs: number;
  grafanaUrl: string | null;
  dashboardUrl: string | null;
  sloUrl: string | null;
  status: ServiceHealthStatus;
  summary: string;
  description: string | null;
  metrics: ServiceHealthMetrics;
  dependencies: ServiceHealthDependency[];
  warnings: string[];
}
