import type { RenderServiceHealthResult, ServiceHealthPoint, ServiceHealthTimeRange } from '../src/service-health';

const RANGE_DURATION_MS: Record<ServiceHealthTimeRange, number> = {
  '1h': 60 * 60 * 1_000,
  '6h': 6 * 60 * 60 * 1_000,
  '24h': 24 * 60 * 60 * 1_000,
  '7d': 7 * 24 * 60 * 60 * 1_000,
};

function points(values: number[], fromMs: number, toMs: number): ServiceHealthPoint[] {
  return values.map((value, index) => ({
    timestampMs: fromMs + (index / (values.length - 1)) * (toMs - fromMs),
    value,
  }));
}

/** Synthetic preview data that matches the service-health design fixture. */
export function makeServiceHealthFixture(timeRange: ServiceHealthTimeRange = '1h'): RenderServiceHealthResult {
  const toMs = Date.UTC(2026, 8, 18, 10);
  const fromMs = toMs - RANGE_DURATION_MS[timeRange];

  return {
    serviceName: 'checkout-service',
    serviceNamespace: 'production',
    datasourceUid: 'prometheus-production',
    timeRange,
    fromMs,
    toMs,
    grafanaUrl: 'https://grafana.example.test/',
    dashboardUrl: 'https://grafana.example.test/d/service-overview/checkout-service',
    sloUrl: 'https://grafana.example.test/a/grafana-slo-app/service/checkout-service',
    status: 'healthy',
    summary: 'checkout-service is healthy. Latency, errors, and traffic are all on their baseline.',
    description: 'Summary of the service overview dashboard. No alerts firing, 2 SLOs on track.',
    metrics: {
      latencyP95: {
        value: 212,
        unit: 'ms',
        points: points(
          [198, 205, 205, 205, 214, 213, 210, 220, 217, 213, 222, 218, 213, 207, 217, 211, 205, 198, 210, 204],
          fromMs,
          toMs
        ),
        unavailableReason: null,
      },
      errorRatio: {
        value: 0.004,
        unit: 'ratio',
        points: points(
          [0.006, 0.0048, 0.0054, 0.0041, 0.005, 0.0035, 0.0042, 0.003, 0.004, 0.0027, 0.0038, 0.0025, 0.0034, 0.0027],
          fromMs,
          toMs
        ),
        unavailableReason: null,
      },
      requestRate: {
        value: 1_200,
        unit: 'requests_per_second',
        points: points(
          [
            1100, 1200, 1180, 1160, 1240, 1210, 1180, 1230, 1190, 1160, 1210, 1170, 1130, 1080, 1150, 1100, 1040, 1120,
            1060,
          ],
          fromMs,
          toMs
        ),
        unavailableReason: null,
      },
    },
    dependencies: [
      {
        name: 'payments-api',
        namespace: 'production',
        type: 'service',
        latencyP95Ms: 38,
        errorRatio: 0.002,
        unavailableReason: null,
      },
      {
        name: 'cart-db',
        namespace: 'production',
        type: 'database',
        latencyP95Ms: 12,
        errorRatio: 0,
        unavailableReason: null,
      },
      {
        name: 'redis',
        namespace: 'production',
        type: 'database',
        latencyP95Ms: 3,
        errorRatio: 0,
        unavailableReason: null,
      },
    ],
    warnings: [],
  };
}
