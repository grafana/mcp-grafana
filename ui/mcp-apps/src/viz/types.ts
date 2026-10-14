/** Shared series types and naming for the visualization components. */

/**
 * Parse a number as Prometheus serialises it.
 *
 * Values and numeric label values arrive as strings, and the infinities use
 * `+Inf` / `-Inf` — which `Number()` reads as `NaN`. Every histogram's last
 * bucket is `le="+Inf"`, so getting this wrong makes bucket detection fail on
 * real data while passing on hand-written fixtures.
 */
export function parsePrometheusNumber(raw: string | undefined): number {
  if (raw === undefined) return Number.NaN;
  switch (raw) {
    case '+Inf':
    case 'Inf':
      return Number.POSITIVE_INFINITY;
    case '-Inf':
      return Number.NEGATIVE_INFINITY;
    default:
      return Number(raw);
  }
}

export type MetricPoint = [timestampMs: number, value: number];

export interface MetricSeries {
  labels: Record<string, string>;
  points: MetricPoint[];
}

/**
 * Human-readable series name, mirroring Grafana's `metric{label="value"}`
 * convention and leading with `__name__`.
 */
export function seriesName(labels: Record<string, string>): string {
  const { __name__: metric, ...rest } = labels ?? {};
  const pairs = Object.keys(rest)
    .sort()
    .map((key) => `${key}="${rest[key]}"`);
  if (!pairs.length) return metric ?? 'value';
  const inner = `{${pairs.join(', ')}}`;
  return metric ? `${metric}${inner}` : inner;
}

/**
 * Names with everything the series share stripped out.
 *
 * A raw PromQL result gives every series the same metric name and mostly the
 * same labels, so full names are unreadable in a legend or on a bar axis —
 * `http_requests_total{route="/api/x"}` five times over, truncated to
 * `http_requests_total{rout…`. Dropping labels identical across all series
 * leaves only what distinguishes them, which is what Grafana does.
 *
 * Falls back to the full name when nothing distinguishes a series (a single
 * series, or genuinely identical label sets).
 */
export function shortSeriesNames(series: MetricSeries[]): string[] {
  if (series.length === 0) return [];
  if (series.length === 1) return [seriesName(series[0].labels)];

  const labelSets = series.map((s) => s.labels ?? {});
  const keys = new Set<string>();
  for (const labels of labelSets) for (const key of Object.keys(labels)) keys.add(key);

  const distinguishing = [...keys].filter((key) => {
    const values = new Set(labelSets.map((labels) => labels[key]));
    return values.size > 1;
  });

  if (distinguishing.length === 0) return labelSets.map(seriesName);

  return labelSets.map((labels) => {
    const parts = distinguishing
      .filter((key) => labels[key] !== undefined)
      .sort()
      .map((key) => (key === '__name__' ? labels[key] : `${key}=${labels[key]}`));
    return parts.length ? parts.join(' · ') : seriesName(labels);
  });
}

/** The metric name every series agrees on, when there is one — drives unit inference. */
export function commonMetricName(series: MetricSeries[]): string | undefined {
  const names = new Set(series.map((s) => s.labels?.__name__).filter(Boolean) as string[]);
  return names.size === 1 ? [...names][0] : undefined;
}

/** Last value of a series, or `NaN` when it has no points. */
export function latestValue(series: MetricSeries | undefined): number {
  const point = series?.points[series.points.length - 1];
  return point ? point[1] : Number.NaN;
}
