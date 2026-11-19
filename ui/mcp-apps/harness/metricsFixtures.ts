// Synthetic Prometheus fixtures retained from the original localhost preview.
export function instantResult(kind: string) {
  const at = Math.floor(Date.now() / 1000);
  const entries: Record<string, Array<[Record<string, string>, string]>> = {
    // Bounded unit, one value → bullet.
    bounded1: [[{ __name__: 'node_memory_utilisation_ratio', instance: 'MacBookPro.mynet' }, '0.87']],
    // Bounded unit, several values → stacked bullets against a shared scale.
    boundedN: [
      [{ __name__: 'node_filesystem_used_ratio', mountpoint: '/' }, '0.42'],
      [{ __name__: 'node_filesystem_used_ratio', mountpoint: '/System/Volumes/Data' }, '0.91'],
      [{ __name__: 'node_filesystem_used_ratio', mountpoint: '/private/var/vm' }, '0.12'],
      [{ __name__: 'node_filesystem_used_ratio', mountpoint: '/Volumes/backup' }, '0.68'],
    ],
    // Unbounded, several values → ranked bars.
    instantN: [
      [{ __name__: 'http_requests_total', route: '/api/dashboards' }, '1842'],
      [{ __name__: 'http_requests_total', route: '/api/datasources' }, '934'],
      [{ __name__: 'http_requests_total', route: '/api/search' }, '712'],
    ],
    // Unbounded, one value → stat.
    single: [[{ __name__: 'go_goroutines', job: 'grafana' }, '1403']],
  };
  const payload = {
    data: (entries[kind] ?? entries.bounded1).map(([metric, value]) => ({ metric, value: [at, value] })),
    exploreUrl: 'https://example.grafana.net/explore?panes=%7B%7D',
  };
  return {
    content: [{ type: 'text' as const, text: JSON.stringify(payload) }],
    structuredContent: payload,
    isError: false,
    _meta: { ui: { resourceUri: 'ui://grafana/app.html' } },
  };
}

/** Cumulative latency buckets over time → heatmap. */
export function bucketResult() {
  const start = Math.floor(Date.now() / 1000) - 3600;
  const bounds = ['0.005', '0.01', '0.025', '0.05', '0.1', '0.25', '0.5', '1', '2.5', '+Inf'];
  const data = bounds.map((le, bucketIndex) => ({
    metric: { __name__: 'http_request_duration_seconds_bucket', le, job: 'api' },
    values: Array.from({ length: 40 }, (_, i) => {
      // A latency shift halfway through: mass moves into the slower buckets.
      const shifted = i > 20 ? 2.5 : 0;
      const centre = 4 + shifted;
      const weight = Math.exp(-((bucketIndex - centre) ** 2) / 4) * 120;
      // Cumulative, as Prometheus reports it.
      const cumulative = bounds
        .slice(0, bucketIndex + 1)
        .reduce((sum, _, j) => sum + Math.exp(-((j - centre) ** 2) / 4) * 120, 0);
      return [start + i * 90, String(Math.round(cumulative + weight * 0))] as [number, string];
    }),
  }));
  const payload = { data, exploreUrl: 'https://example.grafana.net/explore?panes=%7B%7D' };
  return {
    content: [{ type: 'text' as const, text: JSON.stringify(payload) }],
    structuredContent: payload,
    isError: false,
    _meta: { ui: { resourceUri: 'ui://grafana/app.html' } },
  };
}

export function toolResult(generation = 0) {
  const start = Math.floor(Date.now() / 1000) - 3600;
  const values: Array<[number, string]> = Array.from({ length: 60 }, (_, i) => [
    start + i * 60,
    String((2 + Math.sin(i / 4) * 1.5 + (i > 20 && i < 30 ? 30 : 0)).toFixed(3)),
  ]);
  const payload = {
    // The generation offset makes a refresh visibly different from the first render.
    data: [
      {
        metric: { __name__: 'node_load1', instance: 'MacBookPro.mynet' },
        values: values.map(([at, v]) => [at, String((Number(v) + generation).toFixed(3))] as [number, string]),
      },
    ],
    exploreUrl: 'https://example.grafana.net/explore?panes=%7B%7D',
  };
  return {
    content: [{ type: 'text' as const, text: JSON.stringify(payload) }],
    structuredContent: payload,
    isError: false,
    _meta: { ui: { resourceUri: 'ui://grafana/app.html' } },
  };
}
