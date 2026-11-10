import { useEffect, useSyncExternalStore } from 'react';
import type { AppHostState } from './appHostState';
import { AppErrorBoundary } from './AppErrorBoundary';
import { MetricsApplication } from './MetricsApplication';
import { TraceApplication } from './TraceApplication';
import { VizApplication } from './VizApplication';

export function GrafanaApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  useEffect(() => () => host.close(), [host]);
  return (
    <AppErrorBoundary label="Grafana App" colorMode={state.colorMode}>
      {state.toolName === 'query_datasource'
        ? <VizApplication host={host} />
        : state.toolName === 'get_tempo_trace' || state.trace
          ? <TraceApplication host={host} />
          : <MetricsApplication host={host} />}
    </AppErrorBoundary>
  );
}
