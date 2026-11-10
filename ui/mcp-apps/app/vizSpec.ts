import type { McpAppColorMode } from '../src/McpAppShell';

// The viz spec that the App gives the Renderer (viz-contract 2-viz-spec.md, schemas/viz-spec.schema.json).

/** The arguments of query_datasource that the viz spec reads. */
export type VizArgs = {
  datasourceUid: string;
  queries: Array<Record<string, unknown> & { refId: string }>;
};

export type VizPanel = {
  type: string;
  options: Record<string, unknown>;
  fieldConfig?: Record<string, unknown>;
};

/** The structured content of query_datasource (schemas/tool-result.schema.json). */
export type VizResult = {
  chart: string;
  panel: VizPanel;
  alternatives: Array<{ chart: string; panel: VizPanel }>;
  data: { range: { from: number; to: number }; capturedAt: number; results: Record<string, unknown> };
  grafanaUrl?: string;
};

export type VizSpec = {
  specVersion: number;
  panel: VizPanel & { datasource: { uid: string }; targets: VizArgs['queries'] };
  data: VizResult['data'];
  theme?: McpAppColorMode;
  timeZone?: string;
  grafanaUrl?: string;
};

/** The specVersion constant of the contract (spec.1). */
export const SPEC_VERSION = 1;

const isObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

/** Reads the structured content of query_datasource, or gives undefined if it does not have the shape of a result. */
export function parseVizResult(payload: unknown): VizResult | undefined {
  if (!isObject(payload) || typeof payload.chart !== 'string' || !Array.isArray(payload.alternatives)) return undefined;
  const { panel, data } = payload;
  if (!isObject(panel) || typeof panel.type !== 'string' || !isObject(data) || !isObject(data.results)) return undefined;
  return payload as VizResult;
}

/** Reads the query_datasource arguments that the viz spec needs, or gives undefined. */
export function parseVizArgs(args: unknown): VizArgs | undefined {
  if (!isObject(args) || typeof args.datasourceUid !== 'string' || !Array.isArray(args.queries)) return undefined;
  return args as VizArgs;
}

/**
 * Makes the viz spec of a result. `chart` picks an alternative of the result; with no `chart`, or a chart that the
 * result does not have, the spec has the chart of the result.
 */
export function buildVizSpec(
  args: VizArgs,
  result: VizResult,
  host: { theme?: McpAppColorMode; timeZone?: string } = {},
  chart?: string,
): VizSpec {
  const { type, options, fieldConfig } = result.alternatives.find((a) => a.chart === chart)?.panel ?? result.panel;
  return {
    specVersion: SPEC_VERSION,
    panel: {
      // spec.2: a copy of the panel of the result, or of the alternative that the user picked.
      type,
      options,
      ...(fieldConfig ? { fieldConfig } : {}),
      // spec.3: the data source and the queries of the arguments.
      datasource: { uid: args.datasourceUid },
      targets: args.queries,
    },
    // spec.4: the data of the result, with no change.
    data: result.data,
    // spec.5, spec.6: the theme and the time zone of the host context. With none, the Renderer uses its default.
    ...(host.theme ? { theme: host.theme } : {}),
    ...(host.timeZone ? { timeZone: host.timeZone } : {}),
    // spec.7
    ...(result.grafanaUrl ? { grafanaUrl: result.grafanaUrl } : {}),
  };
}
