import type { VizArgs, VizResult } from './vizSpec';

/**
 * The Explore URL of a result: its data source, its queries and the time range of the chart, in the `panes` state of
 * Grafana 10.2+ (as tools/navigation.go). The range is the absolute range of the data, so Explore shows the same
 * data as the chart. Each query gets the data source of the arguments unless it names its own.
 */
export function exploreUrl(grafanaUrl: string, args: VizArgs, range: VizResult['data']['range']): string {
  const datasource = { uid: args.datasourceUid };
  const pane = {
    datasource: args.datasourceUid,
    queries: args.queries.map((query) => ({ ...query, datasource: query.datasource || datasource })),
    range: { from: String(range.from), to: String(range.to) },
  };
  const params = new URLSearchParams({ schemaVersion: '1', panes: JSON.stringify({ a: pane }) });
  return `${grafanaUrl.replace(/\/+$/, '')}/explore?${params}`;
}
