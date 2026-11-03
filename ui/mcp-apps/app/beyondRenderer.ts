// SPIKE option C′: load the grafana-beyond ECharts renderer that the build
// embeds as gzip + base64 (same scheme as the fork's ui/grafana-viz). It is
// decoded only when a query_datasource result arrives, so metrics and trace
// views pay the download but not the parse.

export type VizInput = Record<string, unknown>;
export type RefreshCause = 'refresh' | 'timeRange';

export interface VizHandle {
  update(spec: unknown): void;
  destroy(): void;
  setRefreshed?(ok: boolean, error?: string): void;
}

export interface RenderOptions {
  editable?: boolean;
  zoomOut?: boolean;
  onRefresh?: (input: VizInput, cause: RefreshCause) => void;
}

export interface GrafanaVizApi {
  version: string;
  render(el: HTMLElement, spec: unknown, options?: RenderOptions): VizHandle;
}

declare global {
  interface Window {
    GrafanaViz?: GrafanaVizApi;
  }
}

let load: Promise<GrafanaVizApi> | undefined;

export function loadBeyondRenderer(): Promise<GrafanaVizApi> {
  return (load ??= (async () => {
    const el = document.getElementById('grafana-viz-renderer') as HTMLScriptElement | null;
    if (!el) throw new Error('This build has no Beyond renderer.');
    const gzipped = Uint8Array.from(atob(el.text.trim()), (c) => c.charCodeAt(0));
    const bytes = await new Response(
      new Blob([gzipped]).stream().pipeThrough(new DecompressionStream('gzip')),
    ).arrayBuffer();
    const script = document.createElement('script');
    script.textContent = new TextDecoder().decode(bytes);
    document.head.appendChild(script);
    if (!window.GrafanaViz) throw new Error('The renderer did not set window.GrafanaViz.');
    return window.GrafanaViz;
  })());
}

/** The structuredContent of the query_datasource source (tools/app_dsquery.go). */
export interface DsQueryPayload {
  request: { from: string; to: string; queries: Array<Record<string, unknown>> };
  range: { from: number; to: number };
  capturedAt: number;
  type?: string;
  results: Record<string, unknown>;
}

export function parseDsQueryResult(payload: unknown): DsQueryPayload | undefined {
  const p = payload as Partial<DsQueryPayload> | undefined;
  if (!p || typeof p !== 'object' || !p.request || !p.range || !p.results) return undefined;
  return p as DsQueryPayload;
}

/**
 * Viz spec v1 (contract section 4) from the payload. The panel type is the
 * model's hint or `timeseries` (a core import type). The fork's auto pick
 * (contract 25.2, tools/viz_pick.go) is not ported in this spike.
 */
export function vizSpecFrom(p: DsQueryPayload, theme: 'light' | 'dark'): unknown {
  const queries = p.request.queries;
  const targets = queries.map(({ maxDataPoints: _m, intervalMs: _i, ...t }) => t);
  return {
    specVersion: 1,
    panel: {
      type: p.type || 'timeseries',
      datasource: queries[0]?.datasource ?? null,
      targets,
      maxDataPoints: queries[0]?.maxDataPoints ?? 1000,
      options: {},
      fieldConfig: { defaults: {}, overrides: [] },
    },
    request: { from: String(p.range.from), to: String(p.range.to), queries },
    data: { capturedAt: p.capturedAt, range: p.range, results: p.results },
    theme,
  };
}
