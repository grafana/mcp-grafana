import { describe, expect, it } from 'vitest';

import { axisDecimals, display, formatValue, inferBucketBoundUnit, inferUnit, resolveThresholdColor } from '../src/viz/format';
import { isBoundedUnit, pickViz, type MetricsResult } from '../src/metrics/pickViz';
import { thresholdBands } from '../src/viz/Bullet';
import { deaccumulateBuckets, isHistogramBuckets } from '../src/viz/Heatmap';
import { sparklinePath } from '../src/viz/sparkline';
import {
  grafanaEChartsTheme,
  readThemeColors,
  SERIES_PALETTE,
  seriesColor,
  trackColor,
  withAlpha,
} from '../src/viz/theme';
import { commonMetricName, latestValue, seriesName, shortSeriesNames } from '../src/viz/types';

const series = (labels: Record<string, string>, points: Array<[number, number]>) => ({ labels, points });

describe('formatValue', () => {
  it('scales bytes as IEC', () => {
    expect(formatValue(1530000, 'bytes').formatted).toBe('1.46 MiB');
    expect(formatValue(512, 'bytes').formatted).toBe('512 B');
    expect(formatValue(0, 'bytes').formatted).toBe('0 B');
    expect(formatValue(-2048, 'bytes').formatted).toBe('-2 KiB');
  });

  it('auto-scales seconds across magnitudes', () => {
    expect(formatValue(0.82, 'seconds').formatted).toBe('820 ms');
    expect(formatValue(0.00042, 'seconds').formatted).toBe('420 µs');
    expect(formatValue(1.5e-8, 'seconds').formatted).toBe('15 ns');
    expect(formatValue(45, 'seconds').formatted).toBe('45 s');
    expect(formatValue(5400, 'seconds').formatted).toBe('1.5 hour');
  });

  it('honours explicit decimals but trims when inferring', () => {
    expect(formatValue(1.5, 'none', 2).formatted).toBe('1.50');
    expect(formatValue(1.5, 'none').formatted).toBe('1.5');
  });

  it('handles percent units and non-finite values', () => {
    expect(formatValue(99.4, 'percent').formatted).toBe('99.4%');
    expect(formatValue(0.994, 'percentunit').formatted).toBe('99.4%');
    expect(formatValue(Number.POSITIVE_INFINITY, 'bytes').formatted).toBe('∞');
    expect(formatValue(Number.NaN, 'short').formatted).toBe('NaN');
  });
});

describe('inferUnit', () => {
  it('reads Prometheus naming convention', () => {
    expect(inferUnit('node_cpu_seconds_total')).toBe('seconds');
    expect(inferUnit('go_memstats_alloc_bytes')).toBe('bytes');
    expect(inferUnit('process_cpu_ratio')).toBe('percentunit');
    expect(inferUnit('go_goroutines')).toBe('short');
    expect(inferUnit(undefined)).toBe('none');
  });

  it('never claims a rate: a named series is a raw selector, not rate() output', () => {
    // A running count of requests, not requests per second.
    expect(inferUnit('http_requests_total')).toBe('short');
    expect(inferUnit('prometheus_http_requests_total')).toBe('short');
  });

  it('treats _count and _bucket values as counts of observations', () => {
    expect(inferUnit('http_request_duration_seconds_count')).toBe('short');
    expect(inferUnit('http_request_duration_seconds_bucket')).toBe('short');
    // Only the bucket *bounds* are in the observed unit.
    expect(inferBucketBoundUnit('http_request_duration_seconds_bucket')).toBe('seconds');
    expect(inferUnit('http_request_duration_seconds_sum')).toBe('seconds');
  });
});

describe('thresholds', () => {
  const steps = [
    { value: Number.NEGATIVE_INFINITY, color: 'green' },
    { value: 80, color: 'orange' },
    { value: 90, color: 'red' },
  ];

  it('picks the highest crossed step, inclusive at the boundary', () => {
    expect(resolveThresholdColor(10, steps)).toBe('green');
    expect(resolveThresholdColor(85, steps)).toBe('orange');
    expect(resolveThresholdColor(90, steps)).toBe('red');
    expect(resolveThresholdColor(95, steps)).toBe('red');
    expect(resolveThresholdColor(5, undefined)).toBeUndefined();
  });

  it('combines with formatting in display()', () => {
    const out = display(0.94, {
      unit: 'percentunit',
      thresholds: [
        { value: Number.NEGATIVE_INFINITY, color: '#56c785' },
        { value: 0.9, color: '#e75065' },
      ],
    });
    expect(out.formatted).toBe('94%');
    expect(out.color).toBe('#e75065');
  });
});

describe('pickViz', () => {
  it('derives the viz from the response shape, not from an argument', () => {
    const range: MetricsResult = {
      resultType: 'matrix',
      series: [series({ __name__: 'up', job: 'a' }, [[1, 1], [2, 1], [3, 0]])],
    };
    expect(pickViz(range).kind).toBe('timeseries');
    expect(pickViz(range).alternatives).toEqual(['stat', 'table']);

    const multiRange: MetricsResult = {
      resultType: 'matrix',
      series: [series({ job: 'a' }, [[1, 1], [2, 2]]), series({ job: 'b' }, [[1, 3], [2, 4]])],
    };
    expect(pickViz(multiRange).kind).toBe('timeseries');
    expect(pickViz(multiRange).alternatives).toEqual(['heatmap', 'table']);

    const ranked: MetricsResult = {
      resultType: 'vector',
      series: [series({ job: 'a' }, [[1, 5]]), series({ job: 'b' }, [[1, 3]])],
    };
    expect(pickViz(ranked).kind).toBe('bar');

    expect(pickViz({ resultType: 'matrix', series: [] }).kind).toBe('timeseries');
    expect(pickViz({ resultType: 'vector', series: [series({}, [])] }).kind).toBe('timeseries');
  });

  it('renders a single value as a stat, or a bullet when the unit is bounded', () => {
    const single: MetricsResult = {
      resultType: 'vector',
      series: [series({ __name__: 'go_goroutines' }, [[1, 1400]])],
    };

    // Unbounded: a gauge arc would need min/max Prometheus never supplies.
    expect(pickViz(single, 'short').kind).toBe('stat');
    expect(pickViz(single, 'bytes').kind).toBe('stat');
    expect(pickViz(single).kind).toBe('stat');
    expect(pickViz(single, 'short').alternatives).toEqual(['bar', 'table']);

    // Bounded: the unit itself carries the range.
    expect(pickViz(single, 'percent').kind).toBe('bullet');
    expect(pickViz(single, 'percentunit').kind).toBe('bullet');
    expect(pickViz(single, 'percent').alternatives).toEqual(['stat', 'table']);
  });

  it('compares several bounded values as bullets rather than ranked bars', () => {
    const many: MetricsResult = {
      resultType: 'vector',
      series: [
        series({ instance: 'a' }, [[1, 0.42]]),
        series({ instance: 'b' }, [[1, 0.91]]),
        series({ instance: 'c' }, [[1, 0.12]]),
      ],
    };
    // Ranked bars imply the largest value is the maximum; a shared 0–100% scale does not.
    expect(pickViz(many, 'percentunit').kind).toBe('bullet');
    expect(pickViz(many, 'percentunit').alternatives).toEqual(['bar', 'stat', 'table']);
    // Unbounded values have no shared scale, so ranking is still the honest read.
    expect(pickViz(many, 'short').kind).toBe('bar');
  });

  it('prefers a heatmap for cumulative histogram buckets', () => {
    const buckets: MetricsResult = {
      resultType: 'matrix',
      series: [
        series({ __name__: 'http_request_duration_seconds_bucket', le: '0.1' }, [[1, 5], [2, 6]]),
        series({ __name__: 'http_request_duration_seconds_bucket', le: '0.5' }, [[1, 9], [2, 12]]),
        series({ __name__: 'http_request_duration_seconds_bucket', le: '+Inf' }, [[1, 10], [2, 14]]),
      ],
    };
    // `+Inf` is a number, so the bucket set is recognised.
    expect(pickViz(buckets).kind).toBe('heatmap');
    expect(pickViz(buckets).alternatives).toEqual(['timeseries', 'table']);
  });

  it('prefers a heatmap once there are too many series to overlay', () => {
    const many: MetricsResult = {
      resultType: 'matrix',
      series: Array.from({ length: 24 }, (_, i) => series({ pod: `pod-${i}` }, [[1, i], [2, i + 1]])),
    };
    expect(pickViz(many).kind).toBe('heatmap');

    const few: MetricsResult = {
      resultType: 'matrix',
      series: Array.from({ length: 6 }, (_, i) => series({ pod: `pod-${i}` }, [[1, i], [2, i + 1]])),
    };
    expect(pickViz(few).kind).toBe('timeseries');
    expect(pickViz(few).alternatives).toContain('heatmap');
  });

  it('always offers a table, whatever the response shape', () => {
    const shapes: MetricsResult[] = [
      { resultType: 'matrix', series: [series({ a: '1' }, [[1, 1], [2, 2]])] },
      { resultType: 'matrix', series: [series({ a: '1' }, [[1, 1]]), series({ a: '2' }, [[1, 2]])] },
      { resultType: 'vector', series: [series({ __name__: 'up' }, [[1, 1]])] },
      { resultType: 'matrix', series: [] },
    ];
    for (const shape of shapes) {
      const choice = pickViz(shape);
      expect(choice.alternatives).toContain('table');
      expect(choice.kind).not.toBe('table');
    }
  });

  it('only treats units with an inherent range as bounded', () => {
    expect(isBoundedUnit('percent')).toBe(true);
    expect(isBoundedUnit('percentunit')).toBe(true);
    expect(isBoundedUnit('bytes')).toBe(false);
    expect(isBoundedUnit('seconds')).toBe(false);
    expect(isBoundedUnit(undefined)).toBe(false);
  });

  it('is driven end to end by the inferred unit', () => {
    const ratio: MetricsResult = {
      resultType: 'vector',
      series: [series({ __name__: 'process_cpu_ratio' }, [[1, 0.42]])],
    };
    const bytes: MetricsResult = {
      resultType: 'vector',
      series: [series({ __name__: 'go_memstats_alloc_bytes' }, [[1, 1530000]])],
    };
    const unitOf = (r: MetricsResult) => inferUnit(commonMetricName(r.series));
    expect(pickViz(ratio, unitOf(ratio)).kind).toBe('bullet');
    expect(pickViz(bytes, unitOf(bytes)).kind).toBe('stat');
  });

  it('never defaults to a bullet for an unbounded unit', () => {
    const unbounded: MetricsResult = {
      resultType: 'vector',
      series: [series({ __name__: 'go_goroutines' }, [[1, 1403]])],
    };
    for (const unit of ['short', 'bytes', 'seconds', 'none'] as const) {
      expect(pickViz(unbounded, unit).kind).not.toBe('bullet');
    }
  });
});

describe('histogram buckets', () => {
  const bucket = (le: string, points: Array<[number, number]>) => ({ labels: { le }, points });

  it('recognises a bucket set, including the +Inf catch-all', () => {
    // Number('+Inf') is NaN, so a naive parse rejects every real histogram.
    expect(isHistogramBuckets([bucket('0.1', []), bucket('0.5', []), bucket('+Inf', [])])).toBe(true);
    expect(isHistogramBuckets([bucket('0.1', [])])).toBe(false);
    expect(isHistogramBuckets([{ labels: { pod: 'a' }, points: [] }, { labels: { pod: 'b' }, points: [] }])).toBe(false);
    expect(isHistogramBuckets([bucket('0.1', []), { labels: {}, points: [] }])).toBe(false);
  });

  it('rejects buckets of several histograms interleaved, which cannot be de-accumulated', () => {
    const perInstance = (le: string, instance: string) => ({ labels: { le, instance }, points: [] });
    expect(
      isHistogramBuckets([perInstance('0.1', 'a'), perInstance('+Inf', 'a'), perInstance('0.1', 'b'), perInstance('+Inf', 'b')])
    ).toBe(false);
    // A repeated bound is the same symptom.
    expect(isHistogramBuckets([bucket('0.1', []), bucket('0.1', [])])).toBe(false);
    // Shared labels besides `le` are fine.
    expect(isHistogramBuckets([perInstance('0.1', 'a'), perInstance('+Inf', 'a')])).toBe(true);
  });

  it('turns cumulative counts into per-bucket counts, ordered by bound', () => {
    const rows = deaccumulateBuckets([
      bucket('+Inf', [[1, 10]]),
      bucket('0.1', [[1, 5]]),
      bucket('0.5', [[1, 9]]),
    ]);
    // Sorted by bound, and each row is its own count rather than the running total.
    expect(rows.map((r) => r.labels.le)).toEqual(['0.1', '0.5', '+Inf']);
    expect(rows.map((r) => r.points[0][1])).toEqual([5, 4, 1]);
  });

  it('never reports a negative count when a counter resets', () => {
    const rows = deaccumulateBuckets([bucket('0.1', [[1, 9]]), bucket('0.5', [[1, 4]])]);
    expect(rows.map((r) => r.points[0][1])).toEqual([9, 0]);
  });
});

describe('thresholdBands', () => {
  const steps = [
    { value: Number.NEGATIVE_INFINITY, color: 'green' },
    { value: 80, color: 'orange' },
    { value: 95, color: 'red' },
  ];

  it('spans the scale, with the lowest step as the base', () => {
    const bands = thresholdBands(steps, 0, 100);
    expect(bands).toEqual([
      { fromPct: 0, widthPct: 80, color: 'green' },
      { fromPct: 80, widthPct: 15, color: 'orange' },
      { fromPct: 95, widthPct: 5, color: 'red' },
    ]);
  });

  it('clamps to the scale and drops empty bands', () => {
    // A 0–1 scale with steps expressed in percent: everything collapses to the base.
    const bands = thresholdBands(steps, 0, 1);
    expect(bands).toEqual([{ fromPct: 0, widthPct: 100, color: 'green' }]);
    expect(thresholdBands(undefined, 0, 100)).toEqual([]);
    // A zero-width scale has no bands.
    expect(thresholdBands(steps, 100, 100)).toEqual([]);
  });
});

describe('series naming', () => {
  it('mirrors Grafana label formatting', () => {
    expect(seriesName({ __name__: 'up', job: 'api', instance: 'a:80' })).toBe('up{instance="a:80", job="api"}');
    expect(seriesName({ __name__: 'up' })).toBe('up');
    expect(seriesName({ job: 'api' })).toBe('{job="api"}');
    expect(seriesName({})).toBe('value');
  });

  it('keeps only what distinguishes each series', () => {
    const cpu = [
      series({ __name__: 'node_cpu_seconds_total', instance: 'a', mode: 'user' }, []),
      series({ __name__: 'node_cpu_seconds_total', instance: 'a', mode: 'system' }, []),
    ];
    expect(shortSeriesNames(cpu)).toEqual(['mode=user', 'mode=system']);
  });

  it('falls back to the full name when nothing distinguishes', () => {
    expect(shortSeriesNames([series({ __name__: 'up', job: 'api' }, [])])).toEqual(['up{job="api"}']);
    expect(shortSeriesNames([series({ __name__: 'up' }, []), series({ __name__: 'up' }, [])])).toEqual(['up', 'up']);
    expect(shortSeriesNames([])).toEqual([]);
  });

  it('reports a common metric name only when every series agrees', () => {
    expect(commonMetricName([series({ __name__: 'up' }, []), series({ __name__: 'up' }, [])])).toBe('up');
    expect(commonMetricName([series({ __name__: 'up' }, []), series({ __name__: 'down' }, [])])).toBeUndefined();
    expect(commonMetricName([series({}, [])])).toBeUndefined();
  });

  it('takes the last point as the latest value', () => {
    expect(latestValue(series({}, [[1, 5], [2, 9]]))).toBe(9);
    expect(latestValue(series({}, []))).toBeNaN();
    expect(latestValue(undefined)).toBeNaN();
  });
});

describe('theme', () => {
  it('falls back when custom properties are unavailable', () => {
    const light = readThemeColors(null, 'light');
    const dark = readThemeColors(null, 'dark');
    expect(light.background).not.toBe(dark.background);
    expect(light.border).toBeTruthy();
    expect(dark.border).toBeTruthy();
  });

  it('wires the palette, fonts and 1.5px lines', () => {
    const theme = grafanaEChartsTheme(readThemeColors(null, 'dark'));
    expect(theme.color).toEqual([...SERIES_PALETTE]);
    expect(theme.line.lineStyle.width).toBe(1.5);
    expect(theme.line.symbol).toBe('none');
    expect(theme.textStyle.fontFamily).toMatch(/^Inter/);
    expect(theme.backgroundColor).toBe('transparent');
    // Drag selects a window; it must not hijack the host page's scroll.
    expect(theme.dataZoom[0].zoomOnMouseWheel).toBe(false);
  });

  it('wraps the palette by series index', () => {
    expect(seriesColor(0)).toBe(SERIES_PALETTE[0]);
    expect(seriesColor(SERIES_PALETTE.length)).toBe(SERIES_PALETTE[0]);
  });

  it('derives a track colour that is visible in both modes', () => {
    expect(withAlpha('#ffffff', 0.14)).toBe('rgba(255, 255, 255, 0.14)');
    expect(withAlpha('#FFF', 0.5)).toBe('rgba(255, 255, 255, 0.5)');
    expect(withAlpha('#000000', -1)).toBe('rgba(0, 0, 0, 0)');
    expect(withAlpha('var(--mcp-foreground)', 0.2)).toBe('var(--mcp-foreground)');
    expect(trackColor(readThemeColors(null, 'light'))).not.toBe(trackColor(readThemeColors(null, 'dark')));
  });
});

describe('sparklinePath', () => {
  it('spans the viewBox', () => {
    const path = sparklinePath([
      [0, 10],
      [1, 20],
      [2, 30],
    ]);
    expect(path.startsWith('M0.00,27.00')).toBe(true);
    expect(path.endsWith('L100.00,1.00')).toBe(true);
  });

  it('survives flat and degenerate series', () => {
    expect(
      sparklinePath([
        [0, 5],
        [1, 5],
      ])
    ).toBe('M0.00,14.00 L100.00,14.00');
    expect(sparklinePath([[0, 1]])).toBe('');
    expect(sparklinePath([])).toBe('');
    expect(
      sparklinePath([
        [0, Number.NaN],
        [1, Number.NaN],
      ])
    ).toBe('');
  });
});

describe('axisDecimals', () => {
  it('adds places when auto-rounding would label every tick the same', () => {
    // A scrape-rate series varying only in the fifth decimal place.
    expect(axisDecimals([0.066656, 0.066665, 0.066674])).toBe(6);
  });

  it('defers to auto-rounding for ordinary ranges', () => {
    expect(axisDecimals([0, 0.5, 1])).toBeUndefined();
    expect(axisDecimals([10, 250, 900])).toBeUndefined();
  });

  it('accounts for percentunit being shown multiplied by 100', () => {
    expect(axisDecimals([0.5, 0.50003], 'percentunit')).toBe(4);
  });

  it('leaves flat series and scaled units alone', () => {
    expect(axisDecimals([0.0667, 0.0667])).toBeUndefined();
    expect(axisDecimals([1024, 1024.001], 'bytes')).toBeUndefined();
  });
});
