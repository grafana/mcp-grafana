import { useEffect, useSyncExternalStore } from 'react';
import type { AppHostState } from './appHostState';
import { AppErrorBoundary } from './AppErrorBoundary';
import { MetricsApplication } from './MetricsApplication';
import { TraceApplication } from './TraceApplication';

export function GrafanaApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  useEffect(() => () => host.close(), [host]);
  return (
    <AppErrorBoundary label="Grafana App" colorMode={state.colorMode}>
      {state.toolName === 'get_tempo_trace' || state.trace
        ? <TraceApplication host={host} />
        : <MetricsApplication host={host} />}
    </AppErrorBoundary>
  );
}
