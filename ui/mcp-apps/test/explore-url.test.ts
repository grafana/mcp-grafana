import { describe, expect, it } from 'vitest';

import { exploreUrl } from '../app/exploreUrl';

const range = { from: 1700000000000, to: 1700000900000 };

function panes(url: string) {
  const parsed = new URL(url);
  return { parsed, pane: JSON.parse(parsed.searchParams.get('panes') ?? '{}').a };
}

describe('exploreUrl', () => {
  it('opens the data source, the queries and the range of the data in Explore', () => {
    const { parsed, pane } = panes(
      exploreUrl('https://grafana.example/', { datasourceUid: 'prom', queries: [{ refId: 'A', expr: 'up' }] }, range),
    );

    expect(`${parsed.origin}${parsed.pathname}`).toBe('https://grafana.example/explore');
    expect(parsed.searchParams.get('schemaVersion')).toBe('1');
    expect(pane).toEqual({
      datasource: 'prom',
      queries: [{ refId: 'A', expr: 'up', datasource: { uid: 'prom' } }],
      range: { from: '1700000000000', to: '1700000900000' },
    });
  });

  it('keeps the data source that a query names, with its type', () => {
    const datasource = { uid: 'prom', type: 'prometheus' };
    const { pane } = panes(
      exploreUrl('https://grafana.example', { datasourceUid: 'prom', queries: [{ refId: 'A', datasource }] }, range),
    );
    expect(pane.queries[0].datasource).toEqual(datasource);
  });

  it('keeps the path of a Grafana under a sub path', () => {
    const url = exploreUrl('https://example.com/grafana', { datasourceUid: 'prom', queries: [] }, range);
    expect(new URL(url).pathname).toBe('/grafana/explore');
  });
});
