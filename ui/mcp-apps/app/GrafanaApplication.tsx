import { useEffect, useSyncExternalStore } from 'react';
import type { AppHostState } from './appHostState';
import { AppErrorBoundary } from './AppErrorBoundary';
import sources from './appSources.json';
import { DatasourceApplication } from './DatasourceApplication';
import { MetricsApplication } from './MetricsApplication';
import { TraceApplication } from './TraceApplication';

export function GrafanaApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  useEffect(() => () => host.close(), [host]);
  return (
    <AppErrorBoundary label="Grafana App" colorMode={state.colorMode}>
      {state.toolName === sources.datasource || state.datasource
        ? <DatasourceApplication host={host} />
        : state.toolName === sources.trace || state.trace
          ? <TraceApplication host={host} />
          : <MetricsApplication host={host} />}
    </AppErrorBoundary>
  );
}
