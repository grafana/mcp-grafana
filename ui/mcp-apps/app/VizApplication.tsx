import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';

import type { RenderHandle } from '../renderer/renderer.js';
import { openInGrafanaAction } from '../src/grafanaLink';
import { McpAppShell } from '../src/McpAppShell';
import '../styles.css';
import type { AppHostState } from './appHostState';
import { exploreUrl } from './exploreUrl';
import { loadRenderer } from './loadRenderer';
import { QueryList, queryText } from './QueryList';
import { buildVizSpec, type VizArgs, type VizSpec } from './vizSpec';

/**
 * The summary title: the text of the first query on one line, or the data source when the query has no text.
 * The count of the other queries follows.
 */
export function describeQueries(args: VizArgs): string {
  const [first, ...rest] = args.queries;
  const title = queryText(first)?.replace(/\s+/g, ' ').trim() || `Data source ${args.datasourceUid}`;
  return rest.length ? `${title} (+${rest.length} more)` : title;
}

/** Draws the result of query_datasource with the viz contract Renderer. */
export function VizApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  const { viz, vizArgs, colorMode, timeZone } = state;
  const spec = useMemo(
    () => (viz && vizArgs ? buildVizSpec(vizArgs, viz, { theme: colorMode, timeZone }) : undefined),
    [viz, vizArgs, colorMode, timeZone],
  );
  // spec.7: the link opens the queries in Explore, in the Grafana of the result.
  const grafanaUrl = useMemo(
    () => viz?.grafanaUrl && (vizArgs ? exploreUrl(viz.grafanaUrl, vizArgs, viz.data.range) : viz.grafanaUrl),
    [viz, vizArgs],
  );

  return (
    <McpAppShell
      product="Grafana"
      layout="wide"
      colorMode={colorMode}
      summary={{
        label: 'Data source query',
        title: vizArgs ? describeQueries(vizArgs) : 'Query result',
        // Errors go to the feedback banner; repeating them here reads as a stutter.
        description: state.isError
          ? undefined
          : viz && vizArgs
            ? `${viz.chart} · ${vizArgs.datasourceUid}`
            : state.status,
      }}
      feedback={state.isError ? { tone: 'error', message: state.status } : undefined}
      // The sandbox cannot navigate; the host opens the link.
      openInGrafana={openInGrafanaAction(grafanaUrl, host.openInGrafana)}
    >
      {spec ? <RendererView key={state.generation} spec={spec} /> : null}
      {spec && vizArgs ? <QueryList queries={vizArgs.queries} /> : null}
    </McpAppShell>
  );
}

/**
 * Owns one Renderer handle: render once the module loads, update on a new spec, destroy on unmount (renderer.2 to
 * renderer.5). The module loads on first use (loadRenderer), so the other views and their tests do not load @grafana/ui.
 * A failure there is async, so it is thrown on the next render for AppErrorBoundary.
 */
function RendererView({ spec }: { spec: VizSpec }) {
  const el = useRef<HTMLDivElement>(null);
  const handle = useRef<RenderHandle | null>(null);
  // The spec to draw when the module loads, if it changed while the module was loading.
  const latest = useRef(spec);
  const [failure, setFailure] = useState<Error>();

  useEffect(() => {
    let destroyed = false;
    loadRenderer()
      .then(({ render }) => {
        if (destroyed || !el.current) return;
        handle.current = render(el.current, latest.current);
      })
      .catch((cause: unknown) => {
        if (!destroyed) setFailure(cause instanceof Error ? cause : new Error(String(cause)));
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

  if (failure) throw failure;
  return <div ref={el} data-testid="viz-renderer" />;
}
