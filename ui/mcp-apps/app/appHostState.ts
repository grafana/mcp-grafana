import type { App } from '@modelcontextprotocol/ext-apps';

import type { RefreshRequest, RenderState } from '../renderer/renderer.js';
import type { McpAppColorMode, McpAppFeedback } from '../src/McpAppShell';
import type { ParsedMetrics } from '../src/metrics/pickViz';
import type { TraceViewResult } from '../src/trace/types';
import { applyHostContext } from './hostContext';
import { loadRenderer, type Renderer } from './loadRenderer';
import { extractToolPayload, parsePrometheusResult } from './parsePrometheusResult';
import { parseTraceResult } from './parseTraceResult';
import { vizModelContext } from './vizModelContext';
import { runVizRefresh } from './vizRefresh';
import { parseVizArgs, parseVizResult, type VizArgs, type VizNotice, type VizResult } from './vizSpec';

// Attach handlers before connecting: hosts may deliver results before React's first commit.
/** Source query inputs retained for refresh and range selection. */
export type QueryArgs = {
  expr?: string;
  datasourceUid?: string;
  queryType?: string;
  startTime?: string;
  endTime?: string;
  stepSeconds?: number;
  orgId?: number;
};

export type AppHostSnapshot = {
  result?: ParsedMetrics;
  trace?: TraceViewResult;
  /** The result of query_datasource, which the Renderer draws. */
  viz?: VizResult;
  toolName?: string;
  args?: QueryArgs;
  /** The last arguments of query_datasource, for the viz spec and a refresh (refresh.8). */
  vizArgs?: VizArgs;
  /** The notices of the App for the viz spec of the last refresh only (spec.8, refresh.10). */
  vizNotices?: VizNotice[];
  colorMode: McpAppColorMode;
  /** The IANA time zone of the host context, if it gives one. */
  timeZone?: string;
  status: string;
  isError: boolean;
  /** Transient message about the last user-initiated action. */
  notice?: McpAppFeedback;
  refreshing: boolean;
  /** The host passes tool calls to the server (serverTools), so the viz view can refresh. */
  serverTools: boolean;
  /**
   * Bumped on each tool input, result and cancellation from the host, never on
   * refresh. Keys the view, so state the user set up for one response does not
   * carry into the next, and marks an in-flight refresh as superseded.
   */
  generation: number;
};

export type AppHostState = {
  getSnapshot: () => AppHostSnapshot;
  subscribe: (listener: () => void) => () => void;
  /** Re-run the same query under the host's authenticated MCP session. */
  refresh: () => Promise<void>;
  /**
   * Answers a RefreshRequest of the Renderer (5-refresh.md). A new result goes to the snapshot, and the view calls
   * update. A failure gives the text for setRefreshed. An older request, or one after a new tool call, gives nothing.
   */
  refreshViz: (request: RefreshRequest) => Promise<{ error: string } | undefined>;
  /** Takes a final state or new notices of the Renderer (onState), for the model context (state.14). */
  reportVizState: (state: RenderState) => void;
  /** Hand a brushed window back to the agent as a question. */
  askAboutRange: (fromMs: number, toMs: number) => void;
  /** Ask the host to open a Grafana URL; the iframe cannot navigate itself. */
  openInGrafana: (target: { url: string }) => void;
  /** Load the viz Renderer from its resource on the server, once. */
  loadRenderer: () => Promise<Renderer>;
  close: () => void;
};

const WAITING = 'Waiting for query results…';
const UNAVAILABLE = 'Interactive view unavailable; data remains available in the tool output.';

/** The fields that hold a view. A new call or a failure clears all of them, so no old view stays under the new one. */
const NO_VIEW = { result: undefined, trace: undefined, viz: undefined, vizNotices: undefined } as const;

const error = (message: string): McpAppFeedback => ({ tone: 'error', message });

export function createAppHostState(app: App): AppHostState {
  let snapshot: AppHostSnapshot = {
    colorMode: 'light',
    status: WAITING,
    isError: false,
    refreshing: false,
    serverTools: false,
    generation: 0,
  };
  const listeners = new Set<() => void>();

  const set = (patch: Partial<AppHostSnapshot>) => {
    snapshot = { ...snapshot, ...patch };
    for (const listener of listeners) listener();
  };

  /**
   * Parse a tool result onto the snapshot. Shared by the first render and refresh.
   * `patch` rides along in the same update, so listeners see one change.
   */
  const applyResult = (response: unknown, patch: Partial<AppHostSnapshot> = {}): boolean => {
    const { payload, channel } = extractToolPayload(response);

    if (channel === 'oversized') {
      set({
        ...patch,
        isError: true,
        status: 'This result is too large for the interactive view. The data is in the tool output.',
      });
      return false;
    }

    if (channel === 'none') {
      set({
        ...patch,
        isError: true,
        status:
          'The host delivered a result with no readable payload (neither structuredContent nor a JSON text block).',
      });
      return false;
    }

    if (snapshot.toolName === 'query_datasource') {
      const viz = parseVizResult(payload);
      if (viz && snapshot.vizArgs) {
        set({ ...patch, ...NO_VIEW, viz, isError: false });
        return true;
      }
      set({ ...patch, isError: true, status: UNAVAILABLE });
      return false;
    }

    const trace = snapshot.toolName === 'get_tempo_trace' ? parseTraceResult(payload) : undefined;
    if (trace) {
      set({ ...patch, ...NO_VIEW, trace, isError: false });
      return true;
    }

    const parsed = snapshot.toolName === 'get_tempo_trace' ? undefined : parsePrometheusResult(payload);
    if (!parsed) {
      set({
        ...patch,
        isError: true,
        status: UNAVAILABLE,
      });
      return false;
    }

    set({ ...patch, ...NO_VIEW, result: parsed, isError: false });
    return true;
  };

  // The model context of the viz view (state.14 to state.17): the last state of the Renderer, and whether a context
  // went to the model. Once one did, each new state replaces it, so the model never keeps an old one.
  let vizState: RenderState | undefined;
  let contextSent = false;

  const sendVizContext = () => {
    const { vizArgs, viz } = snapshot;
    if (!vizArgs || !viz || !vizState || !app.getHostCapabilities()?.updateModelContext) return;
    contextSent = true;
    const text = vizModelContext(vizArgs, viz, vizState);
    // The context only helps a later turn; a host that refuses it leaves the view as it is.
    void app.updateModelContext({ content: [{ type: 'text', text }] }).catch(() => {});
  };

  const reportVizState = (state: RenderState) => {
    vizState = state;
    // A first draw that is ready with no notices is what the model asked for (state.16).
    if (state.state !== 'ready' || state.notices.length > 0 || contextSent) sendVizContext();
  };

  /** A new tool call from the host supersedes any refresh still in flight, and starts a new model context. */
  const nextCall = (): Partial<AppHostSnapshot> => {
    vizState = undefined;
    contextSent = false;
    return { generation: snapshot.generation + 1, notice: undefined, refreshing: false };
  };

  app.ontoolinput = (params) => {
    const call = params.arguments;
    const incoming = call?.arguments;
    if (!incoming || typeof incoming !== 'object' || Array.isArray(incoming)) return;
    // The previous chart is not this query's; keeping it would title old data
    // with the new expression until the result arrives.
    const toolName = typeof call.toolName === 'string' ? call.toolName : undefined;
    set({
      ...nextCall(),
      ...NO_VIEW,
      toolName,
      args: { ...incoming, ...(typeof call.orgId === 'number' ? { orgId: call.orgId } : {}) } as QueryArgs,
      vizArgs: toolName === 'query_datasource' ? parseVizArgs(incoming) : undefined,
      isError: false,
      status: WAITING,
    });
  };

  app.ontoolresult = (response) => {
    if (response.isError) {
      set({
        ...nextCall(),
        ...NO_VIEW,
        isError: true,
        status: 'The query failed. Check the query inputs and datasource access, then run render_grafana_app again.',
      });
      return;
    }
    // An unreadable result replaces the previous chart rather than sitting under it.
    applyResult(response, { ...nextCall(), ...NO_VIEW });
  };

  app.ontoolcancelled = () => {
    set({ ...nextCall(), ...NO_VIEW, isError: true, status: 'The query was cancelled.' });
  };

  app.onhostcontextchanged = (context) => {
    const colorMode = applyHostContext(context);
    const timeZone = typeof context.timeZone === 'string' && context.timeZone ? context.timeZone : undefined;
    if (colorMode || timeZone) set({ ...(colorMode ? { colorMode } : {}), ...(timeZone ? { timeZone } : {}) });
  };

  app.onteardown = async () => {
    set({ ...nextCall(), ...NO_VIEW, isError: true, status: 'Grafana App closed.' });
    return {};
  };

  // The host capabilities arrive in the handshake; the Renderer load reads them.
  const connected = app.connect();
  connected
    .then(() => {
      if (app.getHostCapabilities()?.serverTools) set({ serverTools: true });
      // The host context arrives during the handshake; replay it once connected.
      const context = app.getHostContext();
      if (context) app.onhostcontextchanged?.(context);
    })
    .catch(() => {
      set({
        ...NO_VIEW,
        isError: true,
        status: 'Could not connect to the MCP host. Reopen the Grafana App to try again.',
      });
    });

  const refresh = async () => {
    const args = snapshot.args;
    if (!args?.expr || !args.datasourceUid || snapshot.toolName !== 'query_prometheus') return;
    const generation = snapshot.generation;
    // Once the host has delivered a newer call, this refresh's outcome is stale.
    const superseded = () => snapshot.generation !== generation;
    set({ refreshing: true, notice: undefined });
    try {
      const response = await app.callServerTool({ name: snapshot.toolName, arguments: { ...args } });
      if (superseded()) return;
      if (response.isError) {
        set({ notice: error('The refresh query failed. The chart still shows the previous result.') });
      } else if (!applyResult(response)) {
        set({ notice: error('Refresh returned a result the app could not read.') });
      }
    } catch (cause) {
      if (superseded()) return;
      // A host may decline the call, or require an approval the user dismissed.
      set({ notice: error(`Could not refresh: ${cause instanceof Error ? cause.message : String(cause)}`) });
    } finally {
      if (!superseded()) set({ refreshing: false });
    }
  };

  // The id of the last RefreshRequest. Only its result is drawn (refresh.14).
  let lastRefresh: string | undefined;

  const refreshViz = async (request: RefreshRequest) => {
    const { vizArgs, viz, generation } = snapshot;
    if (!vizArgs || !viz) return { error: 'There is no query to refresh.' };
    lastRefresh = request.id;
    const stale = () => lastRefresh !== request.id || snapshot.generation !== generation;
    const orgId = snapshot.args?.orgId;
    const call = (args: VizArgs) =>
      app.callServerTool({
        name: 'render_grafana_app',
        arguments: { toolName: 'query_datasource', arguments: args, ...(orgId === undefined ? {} : { orgId }) },
      });
    try {
      const outcome = await runVizRefresh(call, vizArgs, viz.chart, request, stale);
      if (stale()) return undefined;
      if ('error' in outcome) return outcome;
      set({ vizArgs: outcome.args, viz: outcome.viz, vizNotices: outcome.notices });
      // The model asked for another range, so it hears of the new one (state.14). The Renderer reports a state only
      // when it changes, so this uses the last state; a change after the draw sends the context again.
      if (request.cause === 'timeRange') sendVizContext();
      return undefined;
    } catch (cause) {
      // A host may decline the call, or require an approval the user dismissed.
      return stale() ? undefined : { error: cause instanceof Error ? cause.message : String(cause) };
    }
  };

  const askAboutRange = (fromMs: number, toMs: number) => {
    const expr = snapshot.args?.expr ?? 'this query';
    const from = new Date(fromMs).toISOString();
    const to = new Date(toMs).toISOString();
    void app
      .sendMessage({
        role: 'user',
        content: [
          {
            type: 'text',
            text:
              `Look at \`${expr}\` between ${from} and ${to} and explain what happened in that window. ` +
              `Check related metrics if the cause is not visible in this one.`,
          },
        ],
      })
      .catch(() => set({ notice: error('Could not send the selection to the agent.') }));
  };

  const openInGrafana = ({ url }: { url: string }) => {
    if (!url) return;
    void app
      .openLink({ url })
      .then((response) => {
        set({ notice: response.isError ? error('The host could not open Grafana. Try the link again.') : undefined });
      })
      .catch(() => set({ notice: error('Could not open Grafana. Try the link again.') }));
  };

  return {
    getSnapshot: () => snapshot,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    refresh,
    refreshViz,
    reportVizState,
    askAboutRange,
    openInGrafana,
    // A host may deliver the result before the handshake ends; a failed handshake already shows its own error.
    loadRenderer: () => connected.then(() => loadRenderer(app)),
    close: () => void app.close(),
  };
}
