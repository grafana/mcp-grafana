import { useSyncExternalStore } from 'react';

import { McpAppShell } from '../src/McpAppShell';
import { RenderMetricsApp } from '../src/metrics/RenderMetricsApp';
import '../styles.css';
import { AppErrorBoundary } from './AppErrorBoundary';
import type { MetricsHostState } from './metricsHostState';

/**
 * Renders whatever the host has told us so far.
 *
 * All MCP interaction lives in `metricsHostState`, which attaches its handlers
 * when the `App` is constructed — before React commits — because the host does
 * not replay `tool-input` / `tool-result` and an effect-registered handler
 * misses them whenever the handshake wins the race.
 */
export function MetricsApplication({ host }: { host: MetricsHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  const canRefresh = Boolean(state.args?.expr && state.args.datasourceUid);

  if (state.result) {
    return (
      // The charts are the part that can throw; without a boundary a crash is a blank panel.
      <AppErrorBoundary label="Prometheus query" colorMode={state.colorMode}>
        <RenderMetricsApp
          result={state.result}
          colorMode={state.colorMode}
          expr={state.args?.expr}
          onOpenInGrafana={host.openInGrafana}
          onSelectRange={host.askAboutRange}
          onRefresh={canRefresh ? host.refresh : undefined}
          refreshing={state.refreshing}
          notice={state.notice}
        />
      </AppErrorBoundary>
    );
  }

  return (
    <McpAppShell
      product="Grafana"
      colorMode={state.colorMode}
      summary={{
        label: 'Prometheus query',
        title: state.args?.expr ?? 'Query result',
        // Errors go to the feedback banner; repeating them here reads as a stutter.
        description: state.isError ? undefined : state.status,
      }}
      feedback={state.isError ? { tone: 'error', message: state.status } : undefined}
    />
  );
}
