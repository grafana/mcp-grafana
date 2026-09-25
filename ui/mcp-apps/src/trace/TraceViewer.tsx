import { cx } from '@emotion/css';
import { Button } from '../design';
import {
  type CSSProperties,
  type KeyboardEvent,
  type UIEvent,
  useDeferredValue,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from 'react';

import { getTraceViewerStyles } from './TraceViewer.styles';
import type { RenderTraceResult, TraceAttributeValue, TraceEvent, TraceSpan } from './types';

const ROW_HEIGHT = 48;
const AXIS_HEIGHT = 38;
const DEFAULT_VIEWPORT_HEIGHT = 336;
const OVERSCAN_ROWS = 6;

type FilterMode = 'path' | 'all' | 'errors';
type MobileView = 'trace' | 'span';

type PreparedSpan = TraceSpan & {
  depth: number;
  hasException: boolean;
  sourceIndex: number;
};

interface PreparedTrace {
  orderedSpans: PreparedSpan[];
  byId: Map<string, PreparedSpan>;
  selectedId?: string;
  startTimeMs: number;
  durationMs: number;
  root?: PreparedSpan;
  exceptionCount: number;
  serviceCount: number;
}

export interface TraceViewerProps {
  result: RenderTraceResult;
}

function isErrorSpan(span: TraceSpan) {
  return span.status.toLowerCase().includes('error') || findExceptionEvent(span) !== undefined;
}

function findExceptionEvent(span: TraceSpan): TraceEvent | undefined {
  return span.events.find(
    (event) =>
      event.name.toLowerCase().includes('exception') ||
      Object.keys(event.attributes).some((name) => name.toLowerCase().startsWith('exception.'))
  );
}

function valueToString(value: TraceAttributeValue | undefined): string {
  if (value === undefined) {
    return '';
  }
  if (typeof value === 'string') {
    return value;
  }
  if (value === null || typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function formatDuration(durationMs: number) {
  if (!Number.isFinite(durationMs) || durationMs < 0) {
    return 'Unknown';
  }
  if (durationMs >= 1000) {
    return `${(durationMs / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 })} s`;
  }
  if (durationMs === 0) {
    return '0 ms';
  }
  if (durationMs < 1) {
    return '<1 ms';
  }
  return `${durationMs.toLocaleString(undefined, { maximumFractionDigits: 2 })} ms`;
}

function getAncestorPath(selected: PreparedSpan | undefined, byId: Map<string, PreparedSpan>) {
  const path: PreparedSpan[] = [];
  const pathIds = new Set<string>();
  let cursor = selected;
  while (cursor && !pathIds.has(cursor.id)) {
    pathIds.add(cursor.id);
    path.push(cursor);
    cursor = cursor.parentId ? byId.get(cursor.parentId) : undefined;
  }
  path.reverse();
  return path;
}

function prepareTrace(result: RenderTraceResult): PreparedTrace {
  const source = result.spans.filter(
    (span) => Number.isFinite(span.startTimeMs) && Number.isFinite(span.durationMs) && span.durationMs >= 0
  );
  const byRawId = new Map<string, TraceSpan>();
  for (const span of source) {
    if (!byRawId.has(span.id)) {
      byRawId.set(span.id, span);
    }
  }

  const depthById = new Map<string, number>();
  const calculateDepth = (initial: TraceSpan) => {
    const cached = depthById.get(initial.id);
    if (cached !== undefined) {
      return cached;
    }

    const path: TraceSpan[] = [];
    const seen = new Set<string>();
    let cursor: TraceSpan | undefined = initial;
    let baseDepth = -1;
    while (cursor) {
      const cursorDepth = depthById.get(cursor.id);
      if (cursorDepth !== undefined) {
        baseDepth = cursorDepth;
        break;
      }
      if (seen.has(cursor.id)) {
        baseDepth = -1;
        break;
      }
      seen.add(cursor.id);
      path.push(cursor);
      cursor = cursor.parentId ? byRawId.get(cursor.parentId) : undefined;
    }

    for (let index = path.length - 1; index >= 0; index -= 1) {
      baseDepth += 1;
      depthById.set(path[index].id, baseDepth);
    }
    return depthById.get(initial.id) ?? 0;
  };

  const prepared = source.map<PreparedSpan>((span, sourceIndex) => ({
    ...span,
    sourceIndex,
    depth: calculateDepth(span),
    hasException: findExceptionEvent(span) !== undefined,
  }));
  const byId = new Map(prepared.map((span) => [span.id, span]));
  const sorted = [...prepared].sort(
    (left, right) =>
      left.startTimeMs - right.startTimeMs || right.durationMs - left.durationMs || left.sourceIndex - right.sourceIndex
  );

  // Put children beside their parent when the input contains a usable hierarchy.
  // Orphans and cyclic groups are appended in timeline order without recursing.
  const childrenByParent = new Map<string, PreparedSpan[]>();
  const roots: PreparedSpan[] = [];
  for (const span of sorted) {
    if (span.parentId && span.parentId !== span.id && byId.has(span.parentId)) {
      const children = childrenByParent.get(span.parentId) ?? [];
      children.push(span);
      childrenByParent.set(span.parentId, children);
    } else {
      roots.push(span);
    }
  }
  const orderedSpans: PreparedSpan[] = [];
  const orderedIds = new Set<string>();
  const appendTree = (initial: PreparedSpan) => {
    const stack = [initial];
    while (stack.length > 0) {
      const span = stack.pop();
      if (!span || orderedIds.has(span.id)) {
        continue;
      }
      orderedIds.add(span.id);
      orderedSpans.push(span);
      const children = childrenByParent.get(span.id) ?? [];
      for (let index = children.length - 1; index >= 0; index -= 1) {
        stack.push(children[index]);
      }
    }
  };
  roots.forEach(appendTree);
  sorted.forEach(appendTree);

  const root = roots[0] ?? sorted[0];
  const requestedFocus = result.focusSpanId ? byId.get(result.focusSpanId) : undefined;
  const selected =
    requestedFocus ??
    orderedSpans.find((span) => span.hasException) ??
    orderedSpans.find((span) => span.status.toLowerCase().includes('error')) ??
    root;

  let startTimeMs = 0;
  let endTimeMs = 0;
  if (prepared.length > 0) {
    startTimeMs = prepared[0].startTimeMs;
    endTimeMs = prepared[0].startTimeMs + prepared[0].durationMs;
    for (const span of prepared) {
      startTimeMs = Math.min(startTimeMs, span.startTimeMs);
      endTimeMs = Math.max(endTimeMs, span.startTimeMs + span.durationMs);
    }
  }

  return {
    orderedSpans,
    byId,
    selectedId: selected?.id,
    startTimeMs,
    durationMs: Math.max(0, endTimeMs - startTimeMs),
    root,
    exceptionCount: prepared.filter((span) => span.hasException).length,
    serviceCount: new Set(prepared.map((span) => span.serviceName)).size,
  };
}

function ExceptionDetails({ event, traceStartTimeMs }: { event: TraceEvent; traceStartTimeMs: number }) {
  const styles = getTraceViewerStyles();
  const headingId = `trace-exception-${useId()}`;
  const type = valueToString(event.attributes['exception.type']);
  const message = valueToString(event.attributes['exception.message']);
  const stackTrace = valueToString(event.attributes['exception.stacktrace']);
  const offset = Math.max(0, event.timeMs - traceStartTimeMs);

  return (
    <section className={styles.exception} aria-labelledby={headingId}>
      <h3 id={headingId} className={styles.exceptionHeading}>
        Exception event · +{formatDuration(offset)}
      </h3>
      {(type || message) && (
        <p className={styles.exceptionMessage}>
          {type}
          {type && message ? ': ' : ''}
          {message}
        </p>
      )}
      {stackTrace && (
        <pre className={styles.stackTrace} tabIndex={0} aria-label="Exception stack trace">
          {stackTrace}
        </pre>
      )}
    </section>
  );
}

export function TraceViewer({ result }: TraceViewerProps) {
  const styles = getTraceViewerStyles();
  const rowIdPrefix = `trace-row-${useId()}`;
  const prepared = useMemo(() => prepareTrace(result), [result]);
  const [selectedId, setSelectedId] = useState(prepared.selectedId);
  const [filterMode, setFilterMode] = useState<FilterMode>('path');
  const [query, setQuery] = useState('');
  const deferredQuery = useDeferredValue(query.trim().toLowerCase());
  const [mobileView, setMobileView] = useState<MobileView>('span');
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(DEFAULT_VIEWPORT_HEIGHT);
  const viewportRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setSelectedId(prepared.selectedId);
    setFilterMode('path');
    setQuery('');
    setMobileView('span');
    setScrollTop(0);
  }, [prepared]);

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || typeof ResizeObserver === 'undefined') {
      return;
    }
    const observer = new ResizeObserver(([entry]) =>
      setViewportHeight(entry.contentRect.height || DEFAULT_VIEWPORT_HEIGHT)
    );
    observer.observe(viewport);
    return () => observer.disconnect();
  }, []);

  const selected = (selectedId && prepared.byId.get(selectedId)) || prepared.root;
  const selectedPath = useMemo(() => getAncestorPath(selected, prepared.byId), [prepared.byId, selected]);
  const selectedException = selected ? findExceptionEvent(selected) : undefined;
  const filtered = useMemo(() => {
    const base =
      filterMode === 'path'
        ? selectedPath
        : filterMode === 'errors'
          ? prepared.orderedSpans.filter(isErrorSpan)
          : prepared.orderedSpans;
    if (!deferredQuery) {
      return base;
    }
    return base.filter((span) => `${span.name} ${span.serviceName} ${span.id}`.toLowerCase().includes(deferredQuery));
  }, [deferredQuery, filterMode, prepared, selectedPath]);

  const firstVisible = Math.max(0, Math.floor((scrollTop - AXIS_HEIGHT) / ROW_HEIGHT) - OVERSCAN_ROWS);
  const visibleCount = Math.ceil(viewportHeight / ROW_HEIGHT) + OVERSCAN_ROWS * 2;
  const visibleSpans = filtered.slice(firstVisible, firstVisible + visibleCount);
  const traceDuration = Math.max(prepared.durationMs, 1);
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((fraction) => ({
    fraction,
    label: formatDuration(prepared.durationMs * fraction),
  }));

  const resetScroll = () => {
    setScrollTop(0);
    if (viewportRef.current) {
      viewportRef.current.scrollTop = 0;
    }
  };
  const showFocusPath = () => {
    setFilterMode('path');
    setQuery('');
    resetScroll();
  };
  const showAll = () => {
    setFilterMode('all');
    resetScroll();
  };
  const showErrors = () => {
    setFilterMode('errors');
    setQuery('');
    resetScroll();
  };
  const onScroll = (event: UIEvent<HTMLDivElement>) => setScrollTop(event.currentTarget.scrollTop);
  const selectAtIndex = (index: number) => {
    const next = filtered[index];
    if (!next) {
      return;
    }
    setSelectedId(next.id);
    const viewport = viewportRef.current;
    if (viewport) {
      const rowTop = AXIS_HEIGHT + index * ROW_HEIGHT;
      let nextScrollTop = viewport.scrollTop;
      if (rowTop < viewport.scrollTop + AXIS_HEIGHT) {
        nextScrollTop = Math.max(0, rowTop - AXIS_HEIGHT);
      } else if (rowTop + ROW_HEIGHT > viewport.scrollTop + viewportHeight) {
        nextScrollTop = rowTop + ROW_HEIGHT - viewportHeight;
      }
      viewport.scrollTop = nextScrollTop;
      setScrollTop(nextScrollTop);
    }
  };
  const onWaterfallKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key) || filtered.length === 0) {
      return;
    }
    event.preventDefault();
    const currentIndex = selected ? filtered.findIndex((span) => span.id === selected.id) : -1;
    if (event.key === 'Home') {
      selectAtIndex(0);
    } else if (event.key === 'End') {
      selectAtIndex(filtered.length - 1);
    } else if (event.key === 'ArrowUp') {
      selectAtIndex(Math.max(0, currentIndex < 0 ? 0 : currentIndex - 1));
    } else {
      selectAtIndex(Math.min(filtered.length - 1, currentIndex + 1));
    }
  };

  const attributeEntries = selected
    ? [
        ['field-service', 'Service', selected.serviceName],
        ['field-id', 'Span ID', selected.id],
        ['field-parent', 'Parent span ID', selected.parentId ?? 'None'],
        ['field-start', 'Start offset', formatDuration(Math.max(0, selected.startTimeMs - prepared.startTimeMs))],
        ...Object.entries(selected.attributes)
          .filter(([name]) => name !== 'exception.stacktrace')
          .sort(([left], [right]) => left.localeCompare(right))
          .map(([name, value]) => [`attribute-${name}`, name, valueToString(value)]),
      ]
    : [];

  const statusMessage =
    filterMode === 'path'
      ? `Focused path · ${filtered.length.toLocaleString()} of ${prepared.orderedSpans.length.toLocaleString()} spans`
      : `${filtered.length.toLocaleString()} of ${prepared.orderedSpans.length.toLocaleString()} spans${
          filterMode === 'errors' ? ' · error filter' : ''
        }`;
  const selectedStatus = selected?.status === 'ok' ? 'OK' : selected?.status === 'error' ? 'Error' : 'Unset';

  return (
    <>
      <div className={cx(styles.tabs, styles.responsiveTabs)} role="group" aria-label="Trace view">
        <Button
          aria-pressed={mobileView === 'trace'}
          variant={mobileView === 'trace' ? 'secondary' : 'ghost'}
          size="sm"
          onClick={() => setMobileView('trace')}
        >
          Trace
        </Button>
        <Button
          aria-pressed={mobileView === 'span'}
          variant={mobileView === 'span' ? 'secondary' : 'ghost'}
          size="sm"
          onClick={() => setMobileView('span')}
        >
          {selectedException ? 'Span · Exception' : 'Span details'}
        </Button>
      </div>

      <div className={cx(styles.viewer, styles.responsiveViewer)}>
        <section
          className={cx(styles.tracePane, mobileView === 'trace' ? styles.traceVisible : styles.traceHidden)}
          role="region"
          aria-label="Trace waterfall"
        >
          <div className={styles.paneHeading}>
            <h2 className={styles.heading}>Span waterfall</h2>
            <Button variant="ghost" size="xs" onClick={showFocusPath}>
              Focus selected path
            </Button>
          </div>
          <div className={cx(styles.controls, styles.responsiveControls)}>
            <input
              className={styles.search}
              type="search"
              aria-label="Search spans"
              placeholder="Search service or operation"
              value={query}
              onChange={(event) => {
                setQuery(event.currentTarget.value);
                setFilterMode('all');
                resetScroll();
              }}
            />
            <Button variant="secondary" size="sm" onClick={showAll}>
              Show all spans
            </Button>
            <Button variant="secondary" size="sm" onClick={showErrors}>
              Errors only
            </Button>
          </div>
          <p className={styles.count} role="status" aria-live="polite">
            {statusMessage}
          </p>
          <div
            ref={viewportRef}
            className={styles.viewport}
            style={{
              height: Math.min(DEFAULT_VIEWPORT_HEIGHT, Math.max(80, AXIS_HEIGHT + filtered.length * ROW_HEIGHT)),
            }}
            onScroll={onScroll}
            onKeyDown={onWaterfallKeyDown}
            role="listbox"
            aria-label="Trace spans"
            aria-activedescendant={
              selected && visibleSpans.some((span) => span.id === selected.id)
                ? `${rowIdPrefix}-${selected.sourceIndex}`
                : undefined
            }
            tabIndex={0}
          >
            {filtered.length === 0 ? (
              <div className={styles.empty}>No spans match the current search and filter.</div>
            ) : (
              <div className={styles.waterfall}>
                <div className={cx(styles.axis, styles.responsiveAxis)} aria-hidden="true">
                  <span>Service / operation</span>
                  <span className={styles.ticks}>
                    {ticks.map((tick) => (
                      <span key={tick.fraction}>{tick.label}</span>
                    ))}
                  </span>
                  <span />
                </div>
                <div className={styles.rows} style={{ height: filtered.length * ROW_HEIGHT }}>
                  {visibleSpans.map((span, visibleIndex) => {
                    const rowIndex = firstVisible + visibleIndex;
                    const start = Math.max(
                      0,
                      Math.min(100, ((span.startTimeMs - prepared.startTimeMs) / traceDuration) * 100)
                    );
                    const width = Math.max(0, Math.min(100 - start, (span.durationMs / traceDuration) * 100));
                    const rowStyle = {
                      top: rowIndex * ROW_HEIGHT,
                      '--trace-depth': Math.min(span.depth, 8),
                    } as CSSProperties;
                    const barStyle = {
                      '--trace-start': `${start}%`,
                      '--trace-width': `${width}%`,
                    } as CSSProperties;
                    return (
                      <div
                        key={`${span.id}-${span.sourceIndex}`}
                        id={`${rowIdPrefix}-${span.sourceIndex}`}
                        role="option"
                        className={cx(styles.row, styles.responsiveRow)}
                        style={rowStyle}
                        data-exception={span.hasException || undefined}
                        aria-selected={span.id === selected?.id}
                        aria-label={`${span.serviceName}, ${span.name}${span.hasException ? ', exception' : ''}`}
                        onClick={() => {
                          setSelectedId(span.id);
                          setMobileView('span');
                        }}
                      >
                        <span className={styles.spanName}>
                          <span className={styles.operation}>
                            {span.hasException && (
                              <span className={styles.exceptionMarker} aria-hidden="true">
                                !{' '}
                              </span>
                            )}
                            {span.name}
                          </span>
                          <span className={styles.service}>{span.serviceName}</span>
                        </span>
                        <span className={styles.track} aria-hidden="true">
                          <span className={styles.bar} style={barStyle} />
                        </span>
                        <span className={styles.duration}>{formatDuration(span.durationMs)}</span>
                      </div>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        </section>

        <section
          className={cx(
            styles.detailPane,
            styles.responsiveDetails,
            mobileView === 'span' ? styles.detailVisible : styles.detailHidden
          )}
          role="region"
          aria-label="Selected span"
        >
          {selected ? (
            <>
              <div className={styles.detailHeader}>
                <div className={styles.detailHeadingGroup}>
                  <span className={styles.detailLabel}>Selected span</span>
                  <h2 className={styles.heading}>{selected.name}</h2>
                </div>
                <span className={cx(styles.detailStatus, isErrorSpan(selected) && styles.errorStatus)}>
                  {selectedException ? 'Exception' : selectedStatus} · {formatDuration(selected.durationMs)}
                </span>
              </div>

              {selectedException && (
                <ExceptionDetails event={selectedException} traceStartTimeMs={prepared.startTimeMs} />
              )}

              <dl className={styles.attributes}>
                {attributeEntries.map(([key, name, value]) => (
                  <div key={key} className={styles.attributePair}>
                    <dt className={styles.attributeName}>{name}</dt>
                    <dd className={styles.attributeValue}>{value}</dd>
                  </div>
                ))}
              </dl>
            </>
          ) : (
            <div className={styles.empty}>This trace contains no spans.</div>
          )}
        </section>
      </div>
    </>
  );
}

export function getTraceSummary(result: RenderTraceResult) {
  const prepared = prepareTrace(result);
  const errorCount = prepared.orderedSpans.filter(isErrorSpan).length;
  const parts = [
    formatDuration(prepared.durationMs),
    `${prepared.orderedSpans.length.toLocaleString()} ${prepared.orderedSpans.length === 1 ? 'span' : 'spans'}`,
    `${prepared.serviceCount.toLocaleString()} ${prepared.serviceCount === 1 ? 'service' : 'services'}`,
  ];
  if (prepared.exceptionCount > 0) {
    parts.push(
      `${prepared.exceptionCount.toLocaleString()} exception ${prepared.exceptionCount === 1 ? 'span' : 'spans'}`
    );
  } else if (errorCount > 0) {
    parts.push(`${errorCount.toLocaleString()} error ${errorCount === 1 ? 'span' : 'spans'}`);
  }
  return {
    title: prepared.root?.name || `Trace ${result.traceId}`,
    description: parts.join(' · '),
  };
}
