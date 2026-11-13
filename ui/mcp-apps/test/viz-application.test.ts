import { describe, expect, it } from 'vitest';

import { describeQueries } from '../app/VizApplication';

const args = (...queries: Array<Record<string, unknown>>) => ({
  datasourceUid: 'ds',
  queries: queries.map((query, i) => ({ refId: String.fromCharCode(65 + i), ...query })),
});

describe('describeQueries', () => {
  it('gives the query text', () => {
    expect(describeQueries(args({ expr: 'up' }))).toBe('up');
    expect(describeQueries(args({ query: 'SELECT mean(v) FROM cpu' }))).toBe('SELECT mean(v) FROM cpu');
  });

  it('puts a query of several lines on one line', () => {
    expect(describeQueries(args({ rawSql: 'SELECT time, value\n  FROM metrics\n  WHERE $__timeFilter(time)\n' }))).toBe(
      'SELECT time, value FROM metrics WHERE $__timeFilter(time)',
    );
  });

  it('gives the count of the other queries', () => {
    expect(describeQueries(args({ expr: 'up' }, { expr: 'down' }, { expr: 'sideways' }))).toBe('up (+2 more)');
  });

  it('gives the data source when the query has no text', () => {
    expect(describeQueries(args({ scenarioId: 'random_walk' }))).toBe('Data source ds');
    expect(describeQueries(args({ expr: '  \n ' }))).toBe('Data source ds');
  });
});
