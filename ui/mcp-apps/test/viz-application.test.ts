import { describe, expect, it } from 'vitest';

import { queryTexts } from '../app/VizApplication';
import type { VizArgs } from '../app/vizSpec';

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

  it('gives the target of a Graphite query', () => {
    expect(queryTexts(args({ target: 'sumSeries(app.*.requests)' }))).toEqual([
      { refId: 'A', text: 'sumSeries(app.*.requests)' },
    ]);
  });

  it('leaves out an entry that is not an object, and keeps a query with no refId', () => {
    const queries = [null, 'up', ['up'], { expr: 'up' }] as unknown as VizArgs['queries'];
    expect(queryTexts({ datasourceUid: 'ds', queries })).toEqual([{ refId: '', text: 'up' }]);
  });

  it('gives a query with no text as JSON', () => {
    expect(queryTexts(args({ scenarioId: 'random_walk' }, { expr: '  \n ' }))).toEqual([
      { refId: 'A', text: '{"scenarioId":"random_walk"}' },
      { refId: 'B', text: '{"expr":"  \\n "}' },
    ]);
  });
});
