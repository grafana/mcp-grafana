import { useEffect, useMemo, useRef, useSyncExternalStore } from 'react';

import type { RenderHandle } from '../renderer/renderer.js';
import { McpAppShell } from '../src/McpAppShell';
import '../styles.css';
import type { AppHostState } from './appHostState';
import { buildVizSpec, type VizSpec } from './vizSpec';

/** Draws the result of query_datasource with the viz contract Renderer. */
export function VizApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  const { viz, vizArgs, colorMode, timeZone } = state;
  const spec = useMemo(
    () => (viz && vizArgs ? buildVizSpec(vizArgs, viz, { theme: colorMode, timeZone }) : undefined),
    [viz, vizArgs, colorMode, timeZone],
  );
  const grafanaUrl = viz?.grafanaUrl;

  return (
    <McpAppShell
      product="Grafana"
      layout="wide"
      colorMode={colorMode}
      summary={{
        label: 'Data source query',
        title: viz?.chart ?? 'Query result',
        // Errors go to the feedback banner; repeating them here reads as a stutter.
        description: spec || state.isError ? undefined : state.status,
      }}
      feedback={state.isError ? { tone: 'error', message: state.status } : undefined}
      openInGrafana={
        grafanaUrl
          ? {
              href: grafanaUrl,
              onClick: (event) => {
                // The sandbox cannot navigate; the host opens the link (spec.7).
                event.preventDefault();
                host.openInGrafana({ url: grafanaUrl });
              },
            }
          : undefined
      }
    >
      {spec ? <RendererView key={state.generation} spec={spec} /> : null}
    </McpAppShell>
  );
}

/**
 * Owns one Renderer handle: render once the module loads, update on a new spec, destroy on unmount (renderer.2 to
 * renderer.5). The module loads on first use, so the other views and their tests do not load @grafana/ui.
 */
function RendererView({ spec }: { spec: VizSpec }) {
  const el = useRef<HTMLDivElement>(null);
  const handle = useRef<RenderHandle | null>(null);
  // The spec to draw when the module loads, if it changed while the module was loading.
  const latest = useRef(spec);

  useEffect(() => {
    let destroyed = false;
    void import('../renderer/renderer.js').then(({ render }) => {
      if (destroyed || !el.current) return;
      handle.current = render(el.current, latest.current);
    });
    return () => {
      destroyed = true;
      handle.current?.destroy();
      handle.current = null;
    };
  }, []);

  useEffect(() => {
    latest.current = spec;
    handle.current?.update(spec);
  }, [spec]);

  return <div ref={el} data-testid="viz-renderer" />;
}
