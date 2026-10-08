import { describe, expect, it } from 'vitest';

import { extractToolPayload, parsePrometheusResult } from '../app/parsePrometheusResult';
import { pickViz } from '../src/metrics/pickViz';
import { shortSeriesNames } from '../src/viz/types';
import {
  emptyResult,
  matrixMultiSeries,
  matrixSingleSeries,
  scalarResult,
  vectorMultiSeries,
  vectorSingleBounded,
  vectorSingleUnbounded,
} from './metricsFixtures';

describe('parsePrometheusResult', () => {
  it('discriminates matrix by `values` and converts timestamps to milliseconds', () => {
    const parsed = parsePrometheusResult(matrixMultiSeries);
    expect(parsed).toBeDefined();
    expect(parsed!.resultType).toBe('matrix');
    expect(parsed!.series).toHaveLength(3);
    // The fixture starts at 1760000000 *seconds*; the viz layer works in ms.
    expect(parsed!.series[0].points[0][0]).toBe(1760000000000);
    expect(typeof parsed!.series[0].points[0][1]).toBe('number');
    expect(parsed!.series[0].labels.mode).toBe('user');
  });

  it('discriminates vector by `value` and yields one point per series', () => {
    const parsed = parsePrometheusResult(vectorMultiSeries);
    expect(parsed!.resultType).toBe('vector');
    expect(parsed!.series).toHaveLength(5);
    expect(parsed!.series.every((s) => s.points.length === 1)).toBe(true);
  });

  it('reads a scalar as a bare [seconds, "value"] tuple', () => {
    const parsed = parsePrometheusResult(scalarResult);
    expect(parsed!.resultType).toBe('scalar');
    expect(parsed!.series[0].points).toEqual([[1760000000000, 42]]);
  });

  it('keeps warnings and renders no series for an empty result', () => {
    const parsed = parsePrometheusResult(emptyResult);
    expect(parsed!.series).toHaveLength(0);
    expect(parsed!.warnings).toEqual(['query returned no data for the requested time range']);
  });

  it('rejects non-results so the app can show its fallback', () => {
    expect(parsePrometheusResult(null)).toBeUndefined();
    expect(parsePrometheusResult('nope')).toBeUndefined();
    expect(parsePrometheusResult(42)).toBeUndefined();
    // A recognised envelope with nothing drawable is not a failure.
    expect(parsePrometheusResult({})?.series).toEqual([]);
  });

  it('handles Prometheus special values, which arrive as strings', () => {
    const valueOf = (raw: string) =>
      parsePrometheusResult({ data: [{ metric: {}, value: [1760000000, raw] }] })!.series[0].points[0][1];
    expect(valueOf('1.25')).toBe(1.25);
    expect(valueOf('+Inf')).toBe(Number.POSITIVE_INFINITY);
    expect(valueOf('-Inf')).toBe(Number.NEGATIVE_INFINITY);
    expect(valueOf('NaN')).toBeNaN();
  });
});

describe('parse → pick, end to end', () => {
  const kindOf = (payload: unknown, unit?: Parameters<typeof pickViz>[1]) => {
    const parsed = parsePrometheusResult(payload);
    expect(parsed).toBeDefined();
    return pickViz(parsed!, unit).kind;
  };

  it('drives the expected viz for every response shape', () => {
    expect(kindOf(matrixMultiSeries)).toBe('timeseries');
    expect(kindOf(matrixSingleSeries)).toBe('timeseries');
    expect(kindOf(vectorMultiSeries)).toBe('bar');
    expect(kindOf(vectorSingleUnbounded, 'short')).toBe('stat');
    expect(kindOf(vectorSingleBounded, 'percentunit')).toBe('bullet');
  });

  it('shortens real label sets to the distinguishing part', () => {
    expect(shortSeriesNames(parsePrometheusResult(matrixMultiSeries)!.series)).toEqual([
      'mode=user',
      'mode=system',
      'mode=iowait',
    ]);
    expect(shortSeriesNames(parsePrometheusResult(vectorMultiSeries)!.series)).toEqual([
      'route=/api/dashboards',
      'route=/api/datasources',
      'route=/api/search',
      'route=/api/alerts',
      'route=/api/annotations',
    ]);
  });
});

describe('extractToolPayload', () => {
  it('prefers structuredContent when the host provides it', () => {
    const payload = { data: [] };
    expect(extractToolPayload({ structuredContent: payload, content: [] })).toEqual({
      payload,
      channel: 'structuredContent',
    });
  });

  it('falls back to JSON in a text block, for hosts that strip structuredContent', () => {
    const result = {
      content: [
        { type: 'text', text: 'Query returned 1 series.' },
        { type: 'text', text: JSON.stringify(vectorSingleUnbounded) },
      ],
    };
    const { payload, channel } = extractToolPayload(result);
    expect(channel).toBe('text');
    expect(parsePrometheusResult(payload)?.series).toHaveLength(1);
  });

  it('ignores prose and unparseable blocks rather than throwing', () => {
    const none = { payload: undefined, channel: 'none' };
    expect(extractToolPayload({ content: [{ type: 'text', text: 'no payload here' }] })).toEqual(none);
    expect(extractToolPayload({ content: [{ type: 'text', text: '{ not json' }] })).toEqual(none);
    expect(extractToolPayload({ content: [{ type: 'image', text: undefined }] })).toEqual(none);
    expect(extractToolPayload(undefined)).toEqual(none);
    expect(extractToolPayload('nope')).toEqual(none);
  });
});

describe('exploreUrl', () => {
  it('survives the parse for every response shape', () => {
    const withLink = { ...matrixMultiSeries, exploreUrl: 'https://g.example.com/explore?panes=%7B%7D' };
    expect(parsePrometheusResult(withLink)?.exploreUrl).toBe('https://g.example.com/explore?panes=%7B%7D');
    expect(parsePrometheusResult({ ...vectorMultiSeries, exploreUrl: 'https://g/x' })?.exploreUrl).toBe('https://g/x');
    expect(parsePrometheusResult({ ...scalarResult, exploreUrl: 'https://g/x' })?.exploreUrl).toBe('https://g/x');
    // Empty results still link out, so the user can open the query in Grafana.
    expect(parsePrometheusResult({ ...emptyResult, exploreUrl: 'https://g/x' })?.exploreUrl).toBe('https://g/x');
  });

  it('is absent when the server could not resolve a public URL', () => {
    expect(parsePrometheusResult(matrixMultiSeries)?.exploreUrl).toBeUndefined();
  });
});
