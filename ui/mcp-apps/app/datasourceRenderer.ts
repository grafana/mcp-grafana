/**
 * The any-datasource view: the `query_datasource` payload, the viz spec built
 * from it, and the Grafana ECharts renderer that draws the spec.
 *
 * The renderer is embedded in `dist/app-datasource.html` only, as gzip + base64
 * (vendor/README.md). It is decoded when the first result arrives.
 */

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

export const RENDERER_ELEMENT_ID = 'grafana-echarts-renderer';

let load: Promise<GrafanaVizApi> | undefined;

export function loadDatasourceRenderer(doc: Document = document): Promise<GrafanaVizApi> {
  return (load ??= (async () => {
    const el = doc.getElementById(RENDERER_ELEMENT_ID) as HTMLScriptElement | null;
    if (!el) throw new Error('This server does not serve the datasource view.');
    const gzipped = Uint8Array.from(atob(el.text.trim()), (c) => c.charCodeAt(0));
    const bytes = await new Response(
      new Blob([gzipped]).stream().pipeThrough(new DecompressionStream('gzip')),
    ).arrayBuffer();
    const script = doc.createElement('script');
    script.textContent = new TextDecoder().decode(bytes);
    doc.head.appendChild(script);
    if (!window.GrafanaViz) throw new Error('The renderer did not load.');
    return window.GrafanaViz;
  })());
}

/** The structuredContent of `query_datasource` (tools/app_datasource.go). */
export interface DatasourcePayload {
  request: { from: string; to: string; queries: Array<Record<string, unknown>> };
  range: { from: number; to: number };
  capturedAt: number;
  results: Record<string, unknown>;
  panel: { type: string; options?: Record<string, unknown>; fieldConfig?: Record<string, unknown> };
  pick: { rule: string; type: string };
}

const isObject = (v: unknown): v is Record<string, unknown> =>
  typeof v === 'object' && v !== null && !Array.isArray(v);

export function parseDatasourceResult(payload: unknown): DatasourcePayload | undefined {
  if (!isObject(payload)) return undefined;
  const { request, range, results, panel } = payload;
  if (!isObject(request) || !Array.isArray(request.queries)) return undefined;
  if (!isObject(range) || typeof range.from !== 'number' || typeof range.to !== 'number') return undefined;
  if (!isObject(results) || !isObject(panel) || typeof panel.type !== 'string') return undefined;
  return payload as unknown as DatasourcePayload;
}

/**
 * The viz spec (version 1) that the renderer draws. `panel.targets` and
 * `panel.datasource` come from the request, so the panel can be saved to a
 * dashboard as it is.
 */
export function vizSpecFrom(p: DatasourcePayload, theme: 'light' | 'dark'): unknown {
  const queries = p.request.queries;
  const targets = queries.map(({ maxDataPoints: _m, intervalMs: _i, ...target }) => target);
  return {
    specVersion: 1,
    panel: {
      type: p.panel.type,
      datasource: queries[0]?.datasource ?? null,
      targets,
      maxDataPoints: queries[0]?.maxDataPoints ?? 1000,
      options: p.panel.options ?? {},
      fieldConfig: { defaults: {}, overrides: [], ...(p.panel.fieldConfig ?? {}) },
    },
    request: { from: String(p.range.from), to: String(p.range.to), queries },
    data: { capturedAt: p.capturedAt, range: p.range, results: p.results },
    pick: p.pick,
    theme,
  };
}
