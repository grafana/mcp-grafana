import { cx } from '@emotion/css';
import { Button } from '../design';
import { LoadingIndicator, Pill } from '../design';
import { GlobalCSSVariables } from '../design';
import { type MouseEvent, type ReactNode, useState } from 'react';

import { McpAppSection } from '../McpAppSection';
import { McpAppShell, type McpAppColorMode, type McpAppFeedback } from '../McpAppShell';
import { getServiceHealthStyles, serviceHealthCSSVariables, statusPillColors } from './ServiceHealth.styles';
import type {
  RenderServiceHealthResult,
  ServiceHealthMetric,
  ServiceHealthPoint,
  ServiceHealthStatus,
  ServiceHealthTimeRange,
} from './types';

const TIME_RANGES: ReadonlyArray<{ value: ServiceHealthTimeRange; label: string; axisLabel: string }> = [
  { value: '1h', label: 'Last 1 h', axisLabel: '1 h ago' },
  { value: '6h', label: '6 h', axisLabel: '6 h ago' },
  { value: '24h', label: '24 h', axisLabel: '24 h ago' },
  { value: '7d', label: '7 d', axisLabel: '7 d ago' },
];

const STATUS_LABELS: Record<ServiceHealthStatus, string> = {
  healthy: 'Healthy',
  degraded: 'Degraded',
  unknown: 'Unknown',
};

type MetricKind = 'latency' | 'error' | 'rate';

interface MetricDefinition {
  kind: MetricKind;
  label: string;
  metric: ServiceHealthMetric;
}

interface RenderServiceHealthAppProps {
  result: RenderServiceHealthResult;
  colorMode: McpAppColorMode;
  onTimeRangeChange?: (range: ServiceHealthTimeRange) => void | Promise<void>;
  /** Disables all range controls while the host refreshes the result. */
  pending?: boolean;
  /** Identifies the pending range when the host can expose it. */
  loadingTimeRange?: ServiceHealthTimeRange;
  onNavigate?: (url: string) => void;
  share?: ReactNode;
  feedback?: McpAppFeedback;
}

function formatLatency(value: number | null): string {
  if (value === null) {
    return 'Unavailable';
  }
  return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: value < 10 ? 1 : 0 }).format(value)} ms`;
}

function formatRatio(value: number | null): string {
  if (value === null) {
    return 'Unavailable';
  }
  return new Intl.NumberFormat(undefined, { style: 'percent', maximumFractionDigits: 2 }).format(value);
}

function formatRate(value: number | null): string {
  if (value === null) {
    return 'Unavailable';
  }
  return `${new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 })
    .format(value)
    .toLowerCase()} rps`;
}

function formatMetric(metric: ServiceHealthMetric): string {
  switch (metric.unit) {
    case 'ms':
      return formatLatency(metric.value);
    case 'ratio':
      return formatRatio(metric.value);
    case 'requests_per_second':
      return formatRate(metric.value);
  }
}

type ChartPoint = { x: number; y: number };

function toChartSegments(points: ServiceHealthPoint[], fromMs: number, toMs: number): ChartPoint[][] {
  const validValues = points.flatMap((point) => (point.value === null ? [] : [point.value]));
  if (validValues.length === 0) {
    return [];
  }

  const min = Math.min(...validValues);
  const max = Math.max(...validValues);
  const spread = max - min;
  const segments: ChartPoint[][] = [];
  let current: ChartPoint[] = [];

  const durationMs = Math.max(toMs - fromMs, 1);
  for (const point of points) {
    if (point.value === null) {
      if (current.length > 0) {
        segments.push(current);
        current = [];
      }
      continue;
    }

    current.push({
      x: Math.max(0, Math.min(100, ((point.timestampMs - fromMs) / durationMs) * 100)),
      y: spread === 0 ? 22 : 38 - ((point.value - min) / spread) * 32,
    });
  }

  if (current.length > 0) {
    segments.push(current);
  }
  return segments;
}

function linePath(segment: ChartPoint[]): string {
  return segment.map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' ');
}

function fillPath(segment: ChartPoint[]): string {
  return `${linePath(segment)} L ${segment.at(-1)?.x ?? 0} 44 L ${segment[0]?.x ?? 0} 44 Z`;
}

function segmentKey(segment: ChartPoint[]): string {
  return `${segment[0]?.x ?? 0}-${segment.at(-1)?.x ?? 0}`;
}

function MetricSparkline({
  kind,
  label,
  metric,
  timeRange,
  fromMs,
  toMs,
}: MetricDefinition & { timeRange: ServiceHealthTimeRange; fromMs: number; toMs: number }) {
  const styles = getServiceHealthStyles();
  const segments = toChartSegments(metric.points, fromMs, toMs);
  const range = TIME_RANGES.find((option) => option.value === timeRange) ?? TIME_RANGES[0];
  const hasChart = segments.length > 0;

  return (
    <figure className={styles.metric} data-testid={`service-health-metric-${kind}`}>
      <figcaption className={styles.metricHeader}>
        <span className={styles.metricLabel}>{label}</span>
        <span className={styles.metricValue}>{formatMetric(metric)}</span>
      </figcaption>
      {hasChart ? (
        <svg className={styles.chart} viewBox="0 0 100 44" preserveAspectRatio="none" aria-hidden="true">
          {kind !== 'latency' &&
            segments
              .filter((segment) => segment.length > 1)
              .map((segment) => (
                <path
                  key={`fill-${segmentKey(segment)}`}
                  className={kind === 'error' ? styles.errorFill : styles.rateFill}
                  d={fillPath(segment)}
                />
              ))}
          {segments
            .filter((segment) => segment.length > 1)
            .map((segment) => (
              <path
                key={`line-${segmentKey(segment)}`}
                className={cx(
                  styles.chartLine,
                  kind === 'latency' ? styles.latencyLine : kind === 'error' ? styles.errorLine : styles.rateLine
                )}
                d={linePath(segment)}
              />
            ))}
          {segments
            .filter((segment) => segment.length === 1)
            .map((segment) => (
              <circle
                key={`point-${segmentKey(segment)}`}
                className={
                  kind === 'latency' ? styles.latencyPoint : kind === 'error' ? styles.errorPoint : styles.ratePoint
                }
                cx={segment[0]?.x}
                cy={segment[0]?.y}
                r="2"
                vectorEffect="non-scaling-stroke"
              />
            ))}
        </svg>
      ) : (
        <div className={styles.unavailable}>{metric.unavailableReason ?? 'No data for this time range'}</div>
      )}
      <div className={styles.chartAxis} aria-hidden="true">
        <span>{range.axisLabel}</span>
        <span>now</span>
      </div>
    </figure>
  );
}

function navigationClick(url: string, onNavigate?: (url: string) => void) {
  if (!onNavigate) {
    return undefined;
  }
  return (event: MouseEvent<HTMLAnchorElement>) => {
    event.preventDefault();
    onNavigate(url);
  };
}

export function RenderServiceHealthApp({
  result,
  colorMode,
  onTimeRangeChange,
  pending = false,
  loadingTimeRange,
  onNavigate,
  share,
  feedback,
}: RenderServiceHealthAppProps) {
  const styles = getServiceHealthStyles();
  const [internalLoadingRange, setInternalLoadingRange] = useState<ServiceHealthTimeRange>();
  const pendingRange = loadingTimeRange ?? internalLoadingRange;
  const isPending = pending || Boolean(pendingRange);
  const metrics: MetricDefinition[] = [
    { kind: 'latency', label: 'Latency p95', metric: result.metrics.latencyP95 },
    { kind: 'error', label: 'Error ratio', metric: result.metrics.errorRatio },
    { kind: 'rate', label: 'Request rate', metric: result.metrics.requestRate },
  ];
  const dependencyNameCounts = result.dependencies.reduce<Map<string, number>>((counts, dependency) => {
    counts.set(dependency.name, (counts.get(dependency.name) ?? 0) + 1);
    return counts;
  }, new Map());

  async function changeTimeRange(range: ServiceHealthTimeRange) {
    if (!onTimeRangeChange || isPending || range === result.timeRange) {
      return;
    }
    setInternalLoadingRange(range);
    try {
      await onTimeRangeChange(range);
    } catch {
      // The host owns failure feedback; this component only restores the controls.
    } finally {
      setInternalLoadingRange(undefined);
    }
  }

  return (
    <>
      <GlobalCSSVariables variables={serviceHealthCSSVariables} defaultColorMode="light" />
      <McpAppShell
        product="Grafana Cloud"
        scope={result.serviceName}
        colorMode={colorMode}
        summary={{
          label: 'Service health',
          title: result.summary,
          description: result.description,
          status: <Pill label={STATUS_LABELS[result.status]} size="small" colors={statusPillColors[result.status]} />,
        }}
        openInGrafana={
          result.grafanaUrl
            ? { href: result.grafanaUrl, onClick: navigationClick(result.grafanaUrl, onNavigate) }
            : undefined
        }
        share={share}
        feedback={feedback}
      >
        <McpAppSection
          title="Service overview"
          actions={
            onTimeRangeChange ? (
              <div className={styles.rangeGroup} role="group" aria-label="Service overview time range">
                {TIME_RANGES.map((range) => {
                  const isRangePending = pendingRange === range.value;
                  return (
                    <Button
                      key={range.value}
                      type="button"
                      size="xs"
                      variant={result.timeRange === range.value ? 'secondary' : 'ghost'}
                      aria-pressed={result.timeRange === range.value}
                      aria-busy={isRangePending || undefined}
                      disabled={isPending}
                      onClick={() => void changeTimeRange(range.value)}
                    >
                      {isRangePending && (
                        <span className={styles.rangeLoading} aria-hidden="true">
                          <LoadingIndicator size="xs" />
                        </span>
                      )}
                      {range.label}
                    </Button>
                  );
                })}
              </div>
            ) : undefined
          }
        >
          <div className={styles.overview} aria-busy={isPending}>
            <div className={styles.metrics}>
              {metrics.map((metric) => (
                <MetricSparkline
                  key={metric.kind}
                  {...metric}
                  timeRange={result.timeRange}
                  fromMs={result.fromMs}
                  toMs={result.toMs}
                />
              ))}
            </div>

            {result.dependencies.length > 0 ? (
              <div className={styles.dependencyRegion}>
                <table className={styles.dependencyTable}>
                  <caption className={styles.dependencyCaption}>Outbound &amp; databases</caption>
                  <thead className={styles.dependencyHeader}>
                    <tr>
                      <th scope="col">Name</th>
                      <th scope="col">p95 latency</th>
                      <th scope="col">Error ratio</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.dependencies.map((dependency) => (
                      <tr className={styles.dependencyRow} key={`${dependency.namespace ?? ''}:${dependency.name}`}>
                        <th className={styles.dependencyName} scope="row">
                          {dependency.name}
                          {dependencyNameCounts.get(dependency.name)! > 1 && dependency.namespace && (
                            <span className={styles.dependencyNameDetail}>{dependency.namespace}</span>
                          )}
                          {dependency.unavailableReason && (
                            <span className={styles.dependencyNameDetail}>{dependency.unavailableReason}</span>
                          )}
                        </th>
                        <td className={styles.dependencyValue}>
                          {dependency.latencyP95Ms === null
                            ? 'Unavailable'
                            : `p95 ${formatLatency(dependency.latencyP95Ms)}`}
                        </td>
                        <td className={styles.dependencyValue}>{formatRatio(dependency.errorRatio)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className={styles.empty}>No outbound services or databases were returned.</p>
            )}

            {result.warnings.length > 0 && (
              <aside className={styles.warnings} aria-label="Data notes">
                <h3 className={styles.warningHeading}>Some data is unavailable</h3>
                <ul className={styles.warningList}>
                  {result.warnings.map((warning) => (
                    <li key={warning}>{warning}</li>
                  ))}
                </ul>
              </aside>
            )}
          </div>
        </McpAppSection>

        {(result.dashboardUrl || result.sloUrl) && (
          <div className={styles.actions} aria-label="Service health links">
            {result.dashboardUrl && (
              <Button
                render={<a href={result.dashboardUrl} onClick={navigationClick(result.dashboardUrl, onNavigate)} />}
                nativeButton={false}
                role="link"
                size="sm"
              >
                Open full dashboard
              </Button>
            )}
            {result.sloUrl && (
              <Button
                render={<a href={result.sloUrl} onClick={navigationClick(result.sloUrl, onNavigate)} />}
                nativeButton={false}
                role="link"
                variant="secondary"
                size="sm"
              >
                View SLOs
              </Button>
            )}
          </div>
        )}
      </McpAppShell>
    </>
  );
}

export type { RenderServiceHealthAppProps };
