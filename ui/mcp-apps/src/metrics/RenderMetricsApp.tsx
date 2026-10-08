/**
 * Renders a `query_prometheus` result inside the shared shell.
 *
 * Takes an already-parsed result, so it can be driven by fixtures in the preview
 * harness and by `app.ontoolresult` in the app. MCP wiring lives in
 * `app/MetricsApplication.tsx`.
 */

import { useState } from 'react';
import { SquareDashedMousePointer } from 'lucide-react';

import { getRenderMetricsAppStyles } from './RenderMetricsApp.styles';
import { Button } from '../design';
import { openInGrafanaAction } from '../grafanaLink';
import { McpAppShell, type McpAppColorMode, type McpAppFeedback } from '../McpAppShell';
import { inferUnit, type Threshold, type Unit } from '../viz/format';
import { BarChart } from '../viz/BarChart';
import { Bullet, BulletGroup } from '../viz/Bullet';
import { Heatmap } from '../viz/Heatmap';
import { Stat, StatGroup } from '../viz/Stat';
import { Table } from '../viz/Table';
import { TimeSeriesChart } from '../viz/TimeSeriesChart';
import { commonMetricName, latestValue, seriesName, shortSeriesNames } from '../viz/types';
import { pickViz, type ParsedMetrics, type VizKind } from './pickViz';
import { VizPicker } from './VizPicker';

export interface RenderMetricsAppProps {
  result: ParsedMetrics;
  /** Overrides the Explore link the server supplied on the result. */
  exploreUrl?: string;
  colorMode: McpAppColorMode;
  /** The PromQL that produced this result, when the host passed the tool input. */
  expr?: string;
  /** Overrides the unit inferred from the metric name. */
  unit?: Unit;
  thresholds?: Threshold[];
  /**
   * Opens the Explore link through the host. A sandboxed iframe cannot navigate
   * on its own, so without this the header link does nothing when clicked.
   */
  onOpenInGrafana?: (target: { url: string }) => void;
  /** Hands a brushed time window back to the agent. */
  onSelectRange?: (fromMs: number, toMs: number) => void;
  /** Re-runs the query. Omitted when the host did not report the tool input. */
  onRefresh?: () => void;
  refreshing?: boolean;
  /** Transient message about the last action, shown in the shell's feedback slot. */
  notice?: McpAppFeedback;
  /** Shows the derived choice and its reason. Used by the preview harness. */
  debug?: boolean;
}

export function RenderMetricsApp({
  result,
  colorMode,
  expr,
  exploreUrl,
  unit: unitOverride,
  thresholds,
  onOpenInGrafana,
  onSelectRange,
  onRefresh,
  refreshing = false,
  notice,
  debug = false,
}: RenderMetricsAppProps) {
  const styles = getRenderMetricsAppStyles();
  const metricName = commonMetricName(result.series);
  const unit = unitOverride ?? inferUnit(metricName);
  const choice = pickViz(result, unit);
  // The derived default is the initial state. The user may switch; nothing the
  // model sent influences it.
  const [override, setOverride] = useState<VizKind | undefined>();
  const kind = override ?? choice.kind;
  const options: VizKind[] = [choice.kind, ...choice.alternatives];
  // Brushing is armed from the control row, so the chart carries no UI of its own.
  const [selecting, setSelecting] = useState(false);
  const canSelectRange = Boolean(onSelectRange) && kind === 'timeseries';

  const openInGrafana = openInGrafanaAction(exploreUrl ?? result.exploreUrl, onOpenInGrafana);
  const pointCount = result.series.reduce((total, s) => total + s.points.length, 0);
  // The empty state says this in the body; repeating it here reads as a stutter.
  const description =
    result.series.length === 0
      ? undefined
      : `${result.series.length} series · ${pointCount} point${pointCount === 1 ? '' : 's'}`;
  const title = expr ?? metricName;

  return (
    <McpAppShell
      product="Grafana"
      colorMode={colorMode}
      summary={{
        label: 'Prometheus query',
        // The expression is code; a proportional face misreads it.
        title: title ? <span className={styles.query}>{title}</span> : 'Query result',
        description,
      }}
      openInGrafana={openInGrafana}
      secondaryAction={
        onRefresh
          ? { label: refreshing ? 'Refreshing…' : 'Refresh', onClick: onRefresh, pending: refreshing }
          : undefined
      }
      feedback={notice}
    >
      <div className={styles.controls}>
        <VizPicker options={options} value={kind} onChange={setOverride} />
        {canSelectRange && (
          <Button variant="secondary" size="xs" aria-pressed={selecting} onClick={() => setSelecting((armed) => !armed)}>
            <SquareDashedMousePointer size={12} aria-hidden="true" />
            {selecting ? 'Drag to select' : 'Ask about a window'}
          </Button>
        )}
      </div>

      {renderViz()}

      {result.warnings.map((warning) => (
        <div key={warning} className={styles.warning}>
          {warning}
        </div>
      ))}

      {debug && (
        <div className={styles.debug}>
          {result.resultType} · {choice.kind} · unit={unit} · {choice.reason}
        </div>
      )}
    </McpAppShell>
  );

  function renderViz() {
    if (!result.series.length) {
      return <div className={styles.empty}>No data for this query and time range.</div>;
    }

    switch (kind) {
      case 'timeseries':
        return (
          <TimeSeriesChart
            series={result.series}
            unit={unit}
            colorMode={colorMode}
            selecting={selecting}
            onSelectRange={
              onSelectRange &&
              ((from, to) => {
                setSelecting(false);
                onSelectRange(from, to);
              })
            }
          />
        );
      case 'bar':
        return <BarChart series={result.series} unit={unit} thresholds={thresholds} colorMode={colorMode} />;
      case 'bullet': {
        // Stacked against a shared scale, so several values compare directly.
        const names = shortSeriesNames(result.series);
        return (
          <BulletGroup>
            {result.series.map((series, index) => (
              <Bullet
                key={names[index] || index}
                value={latestValue(series)}
                label={result.series.length === 1 ? (metricName ?? names[index]) : names[index]}
                unit={unit}
                thresholds={thresholds}
                showScale={index === result.series.length - 1}
              />
            ))}
          </BulletGroup>
        );
      }
      case 'heatmap':
        return <Heatmap series={result.series} unit={unit} colorMode={colorMode} />;
      case 'table':
        return <Table series={result.series} unit={unit} />;
      case 'stat': {
        // One stat per series: a single value for an instant query, or the last
        // value plus its history as a sparkline for a range query.
        const names = shortSeriesNames(result.series);
        return (
          <StatGroup>
            {result.series.map((series, index) => (
              <Stat
                key={names[index] || index}
                value={latestValue(series)}
                label={names[index]}
                unit={unit}
                thresholds={thresholds}
                sparkline={series.points.length > 1 ? series.points : undefined}
                colorIndex={index}
              />
            ))}
          </StatGroup>
        );
      }
    }
  }
}
