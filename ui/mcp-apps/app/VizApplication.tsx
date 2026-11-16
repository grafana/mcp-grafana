import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';

import type { RefreshRequest, RenderHandle, RenderState } from '../renderer/renderer.js';
import { McpAppShell } from '../src/McpAppShell';
import '../styles.css';
import type { AppHostState } from './appHostState';
import type { Renderer } from './loadRenderer';
import { buildVizSpec, type VizArgs, type VizSpec } from './vizSpec';

// The query text of the common data sources: Prometheus and Loki, SQL, and others.
const QUERY_TEXT_FIELDS = ['expr', 'rawSql', 'query', 'queryText'];

/**
 * The summary title: the text of the first query on one line, or the data source when the query has no text.
 * The count of the other queries follows.
 */
export function describeQueries(args: VizArgs): string {
  const [first, ...rest] = args.queries;
  const text = QUERY_TEXT_FIELDS.map((name) => first?.[name])
    .map((value) => (typeof value === 'string' ? value.replace(/\s+/g, ' ').trim() : ''))
    .find(Boolean);
  const title = text || `Data source ${args.datasourceUid}`;
  return rest.length ? `${title} (+${rest.length} more)` : title;
}

/** Draws the result of query_datasource with the viz contract Renderer. */
export function VizApplication({ host }: { host: AppHostState }) {
  const state = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
  const { viz, vizArgs, vizNotices, colorMode, timeZone } = state;
  const spec = useMemo(
    () =>
      viz && vizArgs ? buildVizSpec(vizArgs, viz, { theme: colorMode, timeZone }, undefined, vizNotices) : undefined,
    [viz, vizArgs, vizNotices, colorMode, timeZone],
  );
  // A failed load of the Renderer. GrafanaApplication remounts this view for each new result, which clears it.
  const [loadFailure, setLoadFailure] = useState<string>();

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
      feedback={
        state.isError
          ? { tone: 'error', message: state.status }
          : loadFailure
            ? { tone: 'error', message: loadFailure }
            : undefined
      }
    >
      {spec && !loadFailure ? (
        <RendererView
          spec={spec}
          load={host.loadRenderer}
          onLoadError={setLoadFailure}
          // Read when the Renderer draws: the load waits for the handshake, so serverTools is known by then. With no
          // server tool calls a refresh cannot run, so the Renderer shows no Refresh or time picker (refresh.1).
          refresh={() => (host.getSnapshot().serverTools ? host.refreshViz : undefined)}
          onState={host.reportVizState}
        />
      ) : null}
    </McpAppShell>
  );
}

/**
 * Owns one Renderer handle: render once the module loads, update on a new spec, destroy on unmount (renderer.2 to
 * renderer.5). The module loads on first use (loadRenderer), so the other views do not load it. A failed load goes to
 * onLoadError. A failure of render is async, so it is thrown on the next render for AppErrorBoundary. A refresh that
 * gives a result ends with update, through a new spec; a failed one with setRefreshed (refresh.5).
 */
function RendererView({
  spec,
  load,
  onLoadError,
  refresh,
  onState,
}: {
  spec: VizSpec;
  load: () => Promise<Renderer>;
  onLoadError: (message: string) => void;
  refresh: () => ((request: RefreshRequest) => Promise<{ error: string } | undefined>) | undefined;
  onState: (state: RenderState) => void;
}) {
  const el = useRef<HTMLDivElement>(null);
  const handle = useRef<RenderHandle | null>(null);
  // The spec to draw when the module loads, if it changed while the module was loading.
  const latest = useRef(spec);
  const [failure, setFailure] = useState<Error>();

  // Runs once: GrafanaApplication remounts the view for each new result.
  useEffect(() => {
    let destroyed = false;
    load()
      .then(
        ({ render }) => {
          if (destroyed || !el.current) return;
          const onRefresh = refresh();
          try {
            handle.current = render(el.current, latest.current, {
              onState,
              ...(onRefresh && {
                onRefresh: (request: RefreshRequest) => {
                  void onRefresh(request).then((failed) => {
                    if (failed) handle.current?.setRefreshed(request.id, false, failed.error || undefined);
                  });
                },
              }),
            });
          } catch (cause) {
            setFailure(cause instanceof Error ? cause : new Error(String(cause)));
          }
        },
        (cause: unknown) => {
          if (!destroyed) onLoadError(cause instanceof Error ? cause.message : String(cause));
        },
      );
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
