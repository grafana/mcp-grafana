/**
 * Fixtures in the exact shape `query_prometheus` returns — verified by
 * marshalling `model.Matrix` / `model.Vector` / `model.Scalar` with the repo's
 * pinned `prometheus/common v0.72.0`:
 *
 *   matrix  {"data":[{"metric":{…},"values":[[1760000000,"1.25"],…]}]}
 *   vector  {"data":[{"metric":{…},"value":[1760000000,"1"]}]}
 *   scalar  {"data":[1760000000,"42"]}
 *
 * No `resultType`; timestamps in float seconds; values as strings.
 */

const START = 1760000000; // seconds

function ramp(count: number, from: number, to: number, jitter = 0): Array<[number, string]> {
  return Array.from({ length: count }, (_, i) => {
    const t = START + i * 60;
    const base = from + ((to - from) * i) / Math.max(1, count - 1);
    // Deterministic wobble so the preview is stable across runs.
    const wobble = jitter === 0 ? 0 : Math.sin(i / 2.2) * jitter;
    return [t, String(Number((base + wobble).toFixed(4)))] as [number, string];
  });
}

/** Range query, several series — the default time series case. */
export const matrixMultiSeries = {
  data: [
    {
      metric: { __name__: 'node_cpu_seconds_total', instance: '192.168.1.215:9100', mode: 'user' },
      values: ramp(40, 0.18, 0.42, 0.05),
    },
    {
      metric: { __name__: 'node_cpu_seconds_total', instance: '192.168.1.215:9100', mode: 'system' },
      values: ramp(40, 0.09, 0.14, 0.02),
    },
    {
      metric: { __name__: 'node_cpu_seconds_total', instance: '192.168.1.215:9100', mode: 'iowait' },
      values: ramp(40, 0.01, 0.06, 0.012),
    },
  ],
};

/** Range query, one series — defaults to time series, toggles to stat. */
export const matrixSingleSeries = {
  data: [
    {
      metric: { __name__: 'go_memstats_alloc_bytes', job: 'grafana' },
      values: ramp(40, 9_600_000, 15_300_000, 420_000),
    },
  ],
};

/** Instant query, many series — ranked bar. */
export const vectorMultiSeries = {
  data: [
    { metric: { __name__: 'http_requests_total', route: '/api/dashboards' }, value: [START, '1842'] },
    { metric: { __name__: 'http_requests_total', route: '/api/datasources' }, value: [START, '934'] },
    { metric: { __name__: 'http_requests_total', route: '/api/search' }, value: [START, '712'] },
    { metric: { __name__: 'http_requests_total', route: '/api/alerts' }, value: [START, '318'] },
    { metric: { __name__: 'http_requests_total', route: '/api/annotations' }, value: [START, '96'] },
  ],
};

/** Instant query, one value, unbounded unit — stat, not bullet. */
export const vectorSingleUnbounded = {
  data: [{ metric: { __name__: 'go_goroutines', job: 'grafana' }, value: [START, '1403'] }],
};

/** Instant query, one value, bounded unit — bullet is meaningful here. */
export const vectorSingleBounded = {
  data: [{ metric: { __name__: 'node_memory_utilisation_ratio', instance: '192.168.1.215:9100' }, value: [START, '0.87'] }],
};

/** Scalar result. */
export const scalarResult = { data: [START, '42'] };

/** Empty result, with the hints the Go tool attaches. */
export const emptyResult = {
  data: [],
  hints: { reason: 'no series matched the selector' },
  warnings: ['query returned no data for the requested time range'],
};

/** Thresholds only where a metric genuinely has them configured. */
export const RATIO_THRESHOLDS = [
  { value: Number.NEGATIVE_INFINITY, color: '#56c785' },
  { value: 0.8, color: '#ed861d' },
  { value: 0.95, color: '#e75065' },
];

export const FIXTURES = [
  { title: 'Range query · 3 series', payload: matrixMultiSeries },
  { title: 'Range query · 1 series', payload: matrixSingleSeries },
  { title: 'Instant query · 5 series', payload: vectorMultiSeries },
  { title: 'Instant query · 1 value, unbounded unit', payload: vectorSingleUnbounded },
  { title: 'Instant query · 1 value, bounded unit', payload: vectorSingleBounded, thresholds: RATIO_THRESHOLDS },
  { title: 'Scalar', payload: scalarResult },
  { title: 'Empty result', payload: emptyResult },
] as const;
