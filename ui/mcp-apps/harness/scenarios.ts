import { instantResult, bucketResult, toolResult } from './metricsFixtures';
import type { AppBridge } from '@modelcontextprotocol/ext-apps/app-bridge';
import args from '@grafana/viz-contract/fixtures/prometheus-line/args.json';
import result from '@grafana/viz-contract/fixtures/prometheus-line/result.json';
import type { VizSpec } from '../app/vizSpec';
import { trace } from '../preview/traceFixture';

const smokeSpecs = import.meta.glob('../node_modules/@grafana/viz-contract/fixtures/smoke-*/spec.json', {
  eager: true, import: 'default',
}) as Record<string, VizSpec>;

export const chartNames = Object.keys(smokeSpecs).map(path => path.split('/smoke-')[1].split('/')[0]).sort();

let refreshes = 0;
export function refreshResult(name: string) {
  if (name === 'query_prometheus') return toolResult(++refreshes);
  return { content: [], structuredContent: { ...result, panel: { ...result.panel, title: 'Refreshed request rate' } } };
}

export async function deliverScenario(bridge: AppBridge, params: URLSearchParams) {
  const scenario = params.get('scenario') ?? 'viz';
if (scenario === 'metrics') {
  const fixture = params.get('fixture');
  const expressions: Record<string, string> = {
    bounded1: 'node_memory_utilisation_ratio', boundedN: 'node_filesystem_used_ratio',
    instantN: 'topk(3, http_requests_total)', single: 'go_goroutines',
    buckets: 'rate(http_request_duration_seconds_bucket[5m])',
  };
  await bridge.sendToolInput({ arguments: { toolName: 'query_prometheus', arguments: {
    expr: fixture ? (expressions[fixture] ?? fixture) : 'node_load1',
    datasourceUid: 'grafanacloud-prom', queryType: fixture && fixture !== 'buckets' ? 'instant' : 'range',
  } } });
  await bridge.sendToolResult(fixture === 'buckets' ? bucketResult() : fixture ? instantResult(fixture) : toolResult());
} else if (scenario.startsWith('panel')) {
  await bridge.sendToolResult(scenario === 'panel-error' ? {
    content: [{ type: 'text', text: 'Panel rendering failed: fixture error' }], isError: true,
  } : {
    content: [{ type: 'image', mimeType: 'image/png', data: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=' }],
    structuredContent: { deeplink: 'https://example.grafana.net/d/example?viewPanel=1' },
  });
} else if (params.has('chart')) {
  const chart = params.get('chart')!;
  const fixture = smokeSpecs[`../node_modules/@grafana/viz-contract/fixtures/smoke-${chart}/spec.json`];
  if (!fixture) throw new Error(`Missing smoke fixture: ${chart}`);
  const spec = structuredClone(fixture);
  spec.panel.options.animation = { enabled: false };
  await bridge.sendToolInput({ arguments: { toolName: 'query_datasource', arguments: {
    datasourceUid: spec.panel.datasource.uid, queries: spec.panel.targets,
    from: spec.data.range.from, to: spec.data.range.to, type: chart,
  } } });
  await bridge.sendToolResult({ content: [], structuredContent: { chart, panel: spec.panel, data: spec.data, alternatives: [] } });
} else if (scenario === 'trace') {
  await bridge.sendToolInput({ arguments: { toolName: 'get_tempo_trace', arguments: { trace_id: trace.traceId, datasourceUid: trace.datasourceUid } } });
  await bridge.sendToolResult({ content: [], structuredContent: { ...trace, spans: trace.spans.slice(0, 4) } });
} else {
  await bridge.sendToolInput({ arguments: { toolName: 'query_datasource', arguments: args } });
  const payload = structuredClone(result);
  if (scenario === 'empty') payload.data.results.A.frames = [];
  await bridge.sendToolResult(scenario === 'error'
    ? { content: [{ type: 'text', text: 'Data source unavailable' }], isError: true }
    : { content: [], structuredContent: payload });
}
}
