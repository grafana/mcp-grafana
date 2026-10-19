import type { App } from '@modelcontextprotocol/ext-apps';

import type { McpAppColorMode, McpAppFeedback } from '../src/McpAppShell';
import type { ParsedMetrics } from '../src/metrics/pickViz';
import { applyHostContext } from './hostContext';
import { extractToolPayload, parsePrometheusResult } from './parsePrometheusResult';

/**
 * Host state for the metrics app, owned outside React.
 *
 * Handlers must be attached the moment the `App` exists, not in an effect: the
 * host may complete the handshake and fire `tool-input` / `tool-result` before
 * React commits its first render, and those notifications are not replayed. An
 * app that registers handlers in `useEffect` therefore renders correctly or
 * stays stuck on "waiting" depending on timing — which is exactly the kind of
 * bug that looks like a host problem.
 *
 * So the entry point calls `createMetricsHostState(app)` before `render`, and
 * the component subscribes to it.
 */

/** The arguments `query_prometheus` was called with, as the host reports them. */
export type QueryArgs = {
  expr?: string;
  datasourceUid?: string;
  queryType?: string;
  startTime?: string;
  endTime?: string;
  stepSeconds?: number;
};

export type MetricsHostSnapshot = {
  result?: ParsedMetrics;
  args?: QueryArgs;
  colorMode: McpAppColorMode;
  status: string;
  isError: boolean;
  /** Transient message about the last user-initiated action. */
  notice?: McpAppFeedback;
  refreshing: boolean;
  /**
   * Bumped on each tool input, result and cancellation from the host, never on
   * refresh. Keys the view, so state the user set up for one response does not
   * carry into the next, and marks an in-flight refresh as superseded.
   */
  generation: number;
};

export type MetricsHostState = {
  getSnapshot: () => MetricsHostSnapshot;
  subscribe: (listener: () => void) => () => void;
  /** Re-run the same query under the host's authenticated MCP session. */
  refresh: () => Promise<void>;
  /** Hand a brushed window back to the agent as a question. */
  askAboutRange: (fromMs: number, toMs: number) => void;
  /** Ask the host to open a Grafana URL; the iframe cannot navigate itself. */
  openInGrafana: (target: { url: string }) => void;
  close: () => void;
};

const TOOL_NAME = 'query_prometheus';
const WAITING = 'Waiting for query results…';

const error = (message: string): McpAppFeedback => ({ tone: 'error', message });

export function createMetricsHostState(app: App): MetricsHostState {
  let snapshot: MetricsHostSnapshot = {
    colorMode: 'light',
    status: WAITING,
    isError: false,
    refreshing: false,
    generation: 0,
  };
  const listeners = new Set<() => void>();

  const set = (patch: Partial<MetricsHostSnapshot>) => {
    snapshot = { ...snapshot, ...patch };
    for (const listener of listeners) listener();
  };

  /**
   * Parse a tool result onto the snapshot. Shared by the first render and refresh.
   * `patch` rides along in the same update, so listeners see one change.
   */
  const applyResult = (response: unknown, patch: Partial<MetricsHostSnapshot> = {}): boolean => {
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

    const parsed = parsePrometheusResult(payload);
    if (!parsed) {
      set({
        ...patch,
        isError: true,
        status: `Received a payload via ${channel}, but it did not parse as a Prometheus result.`,
      });
      return false;
    }

    set({ ...patch, result: parsed, isError: false });
    return true;
  };

  /** A new tool call from the host supersedes any refresh still in flight. */
  const nextCall = (): Partial<MetricsHostSnapshot> => ({
    generation: snapshot.generation + 1,
    notice: undefined,
    refreshing: false,
  });

  app.ontoolinput = (params) => {
    const incoming = (params as { arguments?: QueryArgs } | undefined)?.arguments;
    if (!incoming || typeof incoming !== 'object') return;
    // The previous chart is not this query's; keeping it would title old data
    // with the new expression until the result arrives.
    set({ ...nextCall(), args: incoming, result: undefined, isError: false, status: WAITING });
  };

  app.ontoolresult = (response) => {
    if (response.isError) {
      set({
        ...nextCall(),
        result: undefined,
        isError: true,
        // The server's error names the bad argument (a missing endTime, an
        // invalid queryType); a fixed message would blame the expression.
        status:
          toolErrorText(response) ??
          'The query failed. Check the expression and datasource access, then run query_prometheus again.',
      });
      return;
    }
    // An unreadable result replaces the previous chart rather than sitting under it.
    applyResult(response, { ...nextCall(), result: undefined });
  };

  app.ontoolcancelled = () => {
    set({ ...nextCall(), result: undefined, isError: true, status: 'The query was cancelled.' });
  };

  app.onhostcontextchanged = (context) => {
    const colorMode = applyHostContext(context);
    if (colorMode) set({ colorMode });
  };

  app.onteardown = async () => {
    set({ result: undefined, status: 'Metrics app closed.' });
    return {};
  };

  app
    .connect()
    .then(() => {
      // The host context arrives during the handshake; replay it once connected.
      const context = app.getHostContext();
      if (context) app.onhostcontextchanged?.(context);
    })
    .catch(() => {
      set({
        result: undefined,
        isError: true,
        status: 'Could not connect to the MCP host. Reopen the metrics app to try again.',
      });
    });

  const refresh = async () => {
    const args = snapshot.args;
    if (!args?.expr || !args.datasourceUid) return;
    const generation = snapshot.generation;
    // Once the host has delivered a newer call, this refresh's outcome is stale.
    const superseded = () => snapshot.generation !== generation;
    set({ refreshing: true, notice: undefined });
    try {
      const response = await app.callServerTool({ name: TOOL_NAME, arguments: { ...args } });
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
    askAboutRange,
    openInGrafana,
    close: () => void app.close(),
  };
}

/** The first text block of a failed tool result, if it carries one. */
function toolErrorText(response: { content?: Array<{ type?: string; text?: string }> }): string | undefined {
  const text = response.content?.find((c) => c.type === 'text' && typeof c.text === 'string')?.text?.trim();
  return text ? `The query failed: ${text}` : undefined;
}
