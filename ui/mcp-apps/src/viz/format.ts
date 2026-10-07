/**
 * Value formatting for MCP app visualizations.
 *
 * Deliberately hand-rolled rather than using `@grafana/data`'s
 * `getDisplayProcessor`: that pulls 1.3 MB of raw JS, and an app bundle ships as
 * a `ui://` protocol resource embedded in the Go binary, so raw size is what
 * travels. This covers the units Prometheus metrics actually carry.
 */

export type Unit =
  | 'none'
  | 'short'
  | 'bytes'
  | 'decbytes'
  | 'bits'
  | 'seconds'
  | 'milliseconds'
  | 'percent'
  | 'percentunit'
  | 'ops'
  | 'reqps';

export type DisplayValue = {
  /** Formatted number, no suffix. */
  text: string;
  /** Unit suffix, already spaced for display (`' MiB'`), or `''`. */
  suffix: string;
  /** `text + suffix`. */
  formatted: string;
  /** Threshold colour, when thresholds were supplied. */
  color?: string;
};

export type Threshold = { value: number; color: string };

const IEC = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB'];
const SI_BYTES = ['B', 'kB', 'MB', 'GB', 'TB', 'PB', 'EB'];
const BITS = ['b', 'kb', 'Mb', 'Gb', 'Tb', 'Pb'];
/** Grafana's `short` unit scale. */
const SHORT = ['', 'K', 'Mil', 'Bil', 'Tri', 'Quadr'];

/** Pick a sensible decimal count when the caller has no opinion. */
function autoDecimals(scaled: number): number {
  const magnitude = Math.abs(scaled);
  if (magnitude === 0) return 0;
  if (magnitude >= 100) return 0;
  if (magnitude >= 10) return 1;
  if (magnitude >= 1) return 2;
  return 3;
}

/** Drop trailing fractional zeros only — `820` must not become `82`. */
function trimFractionalZeros(fixed: string): string {
  if (!fixed.includes('.')) return fixed;
  return fixed.replace(/0+$/, '').replace(/\.$/, '');
}

function round(value: number, decimals?: number): string {
  const places = decimals ?? autoDecimals(value);
  // Trim only when we chose the decimals ourselves — an explicit
  // `decimals: 2` must keep `1.50`.
  const fixed = value.toFixed(places);
  return decimals === undefined ? trimFractionalZeros(fixed) : fixed;
}

function scaleBy(value: number, base: number, units: string[], decimals?: number): DisplayValue {
  const sign = value < 0 ? -1 : 1;
  let magnitude = Math.abs(value);
  let step = 0;
  while (magnitude >= base && step < units.length - 1) {
    magnitude /= base;
    step++;
  }
  const scaled = sign * magnitude;
  const text = round(scaled, decimals);
  const suffix = units[step] ? ` ${units[step]}` : '';
  return { text, suffix, formatted: `${text}${suffix}` };
}

/** Seconds, auto-scaled down to ns and up to days. */
function formatSeconds(value: number, decimals?: number): DisplayValue {
  const magnitude = Math.abs(value);
  const scale = (factor: number, unit: string): DisplayValue => {
    const text = round(value * factor, decimals);
    return { text, suffix: ` ${unit}`, formatted: `${text} ${unit}` };
  };
  if (magnitude === 0) return { text: '0', suffix: ' s', formatted: '0 s' };
  if (magnitude < 1e-6) return scale(1e9, 'ns');
  if (magnitude < 1e-3) return scale(1e6, 'µs');
  if (magnitude < 1) return scale(1e3, 'ms');
  if (magnitude < 60) return scale(1, 's');
  if (magnitude < 3600) return scale(1 / 60, 'min');
  if (magnitude < 86400) return scale(1 / 3600, 'hour');
  return scale(1 / 86400, 'day');
}

export function formatValue(value: number, unit: Unit = 'none', decimals?: number): DisplayValue {
  if (!Number.isFinite(value)) {
    const text = Number.isNaN(value) ? 'NaN' : value > 0 ? '∞' : '-∞';
    return { text, suffix: '', formatted: text };
  }

  switch (unit) {
    case 'bytes':
      return scaleBy(value, 1024, IEC, decimals);
    case 'decbytes':
      return scaleBy(value, 1000, SI_BYTES, decimals);
    case 'bits':
      return scaleBy(value, 1000, BITS, decimals);
    case 'seconds':
      return formatSeconds(value, decimals);
    case 'milliseconds':
      return formatSeconds(value / 1000, decimals);
    case 'percent': {
      const text = round(value, decimals);
      return { text, suffix: '%', formatted: `${text}%` };
    }
    case 'percentunit': {
      const text = round(value * 100, decimals);
      return { text, suffix: '%', formatted: `${text}%` };
    }
    case 'ops':
    case 'reqps': {
      const scaled = scaleBy(value, 1000, SHORT, decimals);
      const label = unit === 'ops' ? 'ops/s' : 'req/s';
      const suffix = `${scaled.suffix} ${label}`.replace(/\s+/g, ' ');
      return { text: scaled.text, suffix, formatted: `${scaled.text}${suffix}` };
    }
    case 'short':
      return scaleBy(value, 1000, SHORT, decimals);
    case 'none':
    default: {
      const text = round(value, decimals);
      return { text, suffix: '', formatted: text };
    }
  }
}

/**
 * Guess a unit from a Prometheus metric name.
 *
 * `query_prometheus` returns no field config, so without this every metric
 * renders as a bare number. Prometheus naming convention (base unit as the
 * last suffix, `_total` for counters) makes this reliable enough to be a
 * better default than `none`, and it stays overridable.
 */
export function inferUnit(metricName: string | undefined): Unit {
  if (!metricName) return 'none';
  // Strip counter/aggregation suffixes that sit after the base unit.
  const name = metricName.toLowerCase().replace(/_(total|sum|count|bucket)$/, '');
  if (/_seconds?$/.test(name)) return 'seconds';
  if (/_milliseconds?$|_ms$/.test(name)) return 'milliseconds';
  if (/_bytes?$/.test(name)) return 'bytes';
  if (/_bits?$/.test(name)) return 'bits';
  if (/_ratio$/.test(name)) return 'percentunit';
  if (/_percent$/.test(name)) return 'percent';
  if (/_(ops|operations)$/.test(name)) return 'ops';
  if (/_requests?$/.test(name)) return 'reqps';
  return 'short';
}

/**
 * Resolve a value against threshold steps. Steps need not be sorted; the
 * lowest step acts as the base and matches everything below the next one.
 */
export function resolveThresholdColor(value: number, thresholds?: Threshold[]): string | undefined {
  if (!thresholds?.length || !Number.isFinite(value)) return undefined;
  const sorted = [...thresholds].sort((a, b) => a.value - b.value);
  let color = sorted[0].color;
  for (const step of sorted) {
    if (value >= step.value) color = step.color;
    else break;
  }
  return color;
}

/** `formatValue` plus threshold resolution — the display path for gauges and bars. */
export function display(
  value: number,
  opts: { unit?: Unit; decimals?: number; thresholds?: Threshold[] } = {}
): DisplayValue {
  const formatted = formatValue(value, opts.unit ?? 'none', opts.decimals);
  const color = resolveThresholdColor(value, opts.thresholds);
  return color ? { ...formatted, color } : formatted;
}
