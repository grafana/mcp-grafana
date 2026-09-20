// Type surface of the vendored @grafana/embed bundle. Importing it defines the
// <grafana-panel> custom element as a side effect.
declare module "*/grafana-embed.mjs" {
  export interface EmbedDataProvider {
    subscribe(onData: (data: unknown) => void): () => void;
    query?(request: { timeRange: unknown; maxDataPoints: number }): void;
  }
  export function framesFromPromMatrix(
    matrix: Array<{ metric: Record<string, string>; values?: Array<[number, string]> }>,
    opts?: { unit?: string; fallbackName?: string }
  ): unknown[];
  /** A /api/ds/query response body: { results: { <refId>: { frames } } }. */
  export function framesFromQueryResponse(body: {
    results: Record<string, { frames?: unknown[] } | undefined>;
  }): unknown[];
  export function staticDataProvider(frames: unknown[]): EmbedDataProvider;
  export function registeredPanelTypes(): string[];
}

interface GrafanaPanelElement extends HTMLElement {
  panel: unknown;
  frames: unknown[];
  dataProvider: unknown;
  refreshTheme(): void;
}
