import { describe, expect, it } from 'vitest';

import { queryTexts } from '../app/VizApplication';

const args = (...queries: Array<Record<string, unknown>>) => ({
  datasourceUid: 'ds',
  queries: queries.map((query, i) => ({ refId: String.fromCharCode(65 + i), ...query })),
});

describe('queryTexts', () => {
  it('gives the text of each query with its refId', () => {
    expect(queryTexts(args({ expr: 'up', legendFormat: '{{job}}' }, { query: 'SELECT mean(v) FROM cpu' }))).toEqual([
      { refId: 'A', text: 'up' },
      { refId: 'B', text: 'SELECT mean(v) FROM cpu' },
    ]);
  });

  it('keeps the line breaks of a query, with no blank lines around it', () => {
    expect(queryTexts(args({ rawSql: '\nSELECT time, value\n  FROM metrics\n' }))).toEqual([
      { refId: 'A', text: 'SELECT time, value\n  FROM metrics' },
    ]);
  });

  it('gives a query with no text as JSON', () => {
    expect(queryTexts(args({ scenarioId: 'random_walk' }, { expr: '  \n ' }))).toEqual([
      { refId: 'A', text: '{"scenarioId":"random_walk"}' },
      { refId: 'B', text: '{"expr":"  \\n "}' },
    ]);
  });
});
