import { useSyncExternalStore } from 'react';
import type { AppHostState } from './appHostState';
import { McpAppShell } from '../src/McpAppShell';
import { openInGrafanaAction } from '../src/grafanaLink';
import { LogsView } from '../src/logs/LogsView';

export function LogsApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  const result = state.logs;
  const metadata = result?.logs.metadata;
  const count = result?.logs.data.filter(
    (entry) => entry.value === undefined && entry.values === undefined
  ).length;
  return (
    <McpAppShell
      product="Grafana Logs"
      layout="wide"
      colorMode={state.colorMode}
      summary={{
        label: 'Loki query',
        title: state.args?.logql ?? result?.query ?? 'Logs',
        description: result ? (
          <>
            {count} log {count === 1 ? 'line' : 'lines'}
            {metadata?.startTime && metadata.endTime && (
              <>
                {' '}
                · {metadata.startTime} – {metadata.endTime}
              </>
            )}
          </>
        ) : state.isError ? undefined : (
          state.status
        ),
      }}
      primaryAction={
        (result || state.isError) && state.args?.logql && state.args.datasourceUid
          ? {
              label: state.isError ? 'Retry' : 'Refresh',
              pending: state.refreshing,
              onClick: () => void host.refresh(),
            }
          : undefined
      }
      openInGrafana={openInGrafanaAction(result?.exploreUrl, host.openInGrafana)}
      feedback={state.notice ?? (state.isError ? { tone: 'error', message: state.status } : undefined)}
    >
      {result && <LogsView key={state.generation} result={result} />}
    </McpAppShell>
  );
}
