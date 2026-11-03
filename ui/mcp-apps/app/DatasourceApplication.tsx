import { useEffect, useRef, useState, useSyncExternalStore } from 'react';

import { LoadingIndicator } from '../src/design';
import { McpAppShell } from '../src/McpAppShell';
import '../styles.css';
import type { AppHostState } from './appHostState';
import { loadDatasourceRenderer, vizSpecFrom, type VizHandle } from './datasourceRenderer';

/** Draws a `query_datasource` result with the Grafana ECharts renderer. */
export function DatasourceApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  const el = useRef<HTMLDivElement>(null);
  const handle = useRef<VizHandle | undefined>(undefined);
  const [renderError, setRenderError] = useState<string>();
  const payload = state.datasource;

  useEffect(() => {
    if (!payload || !el.current) return;
    let cancelled = false;
    loadDatasourceRenderer()
      .then((api) => {
        if (cancelled || !el.current) return;
        const spec = vizSpecFrom(payload, state.colorMode === 'dark' ? 'dark' : 'light');
        if (handle.current) {
          handle.current.update(spec);
          handle.current.setRefreshed?.(true);
          return;
        }
        handle.current = api.render(el.current, spec, {
          editable: false,
          zoomOut: false,
          // The renderer's Refresh and time picker re-run the query in this view.
          onRefresh: (input, cause) =>
            void host
              .refreshDatasource(cause === 'timeRange' ? { from: String(input.from), to: String(input.to) } : {})
              .then((ok) => {
                if (!ok) handle.current?.setRefreshed?.(false, 'The refresh query failed.');
              }),
        });
      })
      .catch((cause) => setRenderError(cause instanceof Error ? cause.message : String(cause)));
    return () => {
      cancelled = true;
    };
  }, [payload, state.colorMode, host]);

  useEffect(
    () => () => {
      handle.current?.destroy();
      handle.current = undefined;
    },
    [],
  );

  if (payload && !renderError) {
    return <div ref={el} style={{ height: 420 }} data-testid="datasource-viz" />;
  }
  const failed = state.isError || Boolean(renderError);
  return (
    <McpAppShell
      product="Grafana"
      colorMode={state.colorMode}
      summary={{ label: 'Datasource query', title: failed ? 'Query view unavailable' : 'Loading query…' }}
      feedback={failed ? { tone: 'error', message: renderError ?? state.status } : undefined}
    >
      {!failed && <div role="status" aria-label="Loading query"><LoadingIndicator /></div>}
    </McpAppShell>
  );
}
