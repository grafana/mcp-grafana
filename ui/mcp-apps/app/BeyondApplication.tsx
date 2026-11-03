import { useEffect, useRef, useState, useSyncExternalStore } from 'react';

import type { AppHostState } from './appHostState';
import { loadBeyondRenderer, vizSpecFrom, type VizHandle } from './beyondRenderer';

/** SPIKE option C′: the grafana-beyond renderer for query_datasource results. */
export function BeyondApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  const el = useRef<HTMLDivElement>(null);
  const handle = useRef<VizHandle | undefined>(undefined);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const payload = state.beyond;
    if (!payload || !el.current) return;
    let cancelled = false;
    loadBeyondRenderer()
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
          // Contract 19.2: the renderer gives the new range; the source re-runs it.
          onRefresh: (input, cause) =>
            void host
              .refreshDatasource(cause === 'timeRange' ? { from: String(input.from), to: String(input.to) } : {})
              .then((ok) => {
                if (!ok) handle.current?.setRefreshed?.(false, 'The refresh query failed.');
              }),
        });
      })
      .catch((cause) => setError(cause instanceof Error ? cause.message : String(cause)));
    return () => {
      cancelled = true;
    };
  }, [state.beyond, state.colorMode, host]);

  useEffect(() => () => handle.current?.destroy(), []);

  return (
    <div style={{ height: 420 }}>
      {error || (!state.beyond && state.status) ? (
        <div role="status" style={{ padding: 12, fontFamily: 'sans-serif' }}>
          {error ?? state.status}
        </div>
      ) : null}
      <div ref={el} style={{ height: '100%' }} data-testid="beyond-viz" />
    </div>
  );
}
