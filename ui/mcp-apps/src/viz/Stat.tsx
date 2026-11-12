import type { CSSProperties } from 'react';

import { display, type Threshold, type Unit } from './format';
import { seriesColor } from './theme';
import { sparklinePath } from './sparkline';
import type { MetricPoint } from './types';
import { getVizStyles } from './Viz.styles';

/**
 * Big-number stat with an optional sparkline.
 *
 * Deliberately **ECharts-free** — DOM plus an inline SVG polyline, zero
 * dependencies beyond React. The Cloud alert-status and service-health apps are
 * bottom-line-first (headline value or health at the top), so this is the
 * component they need most; keeping it dependency-free means they can adopt it
 * without inheriting the chart bundle.
 */

export type StatProps = {
  value: number;
  label?: string;
  unit?: Unit;
  decimals?: number;
  thresholds?: Threshold[];
  /** Optional recent history, drawn as a sparkline under the value. */
  sparkline?: MetricPoint[];
  /** Index into the series palette, used when no threshold colour applies. */
  colorIndex?: number;
  align?: 'left' | 'center';
};

export function Stat({
  value,
  label,
  unit = 'none',
  decimals,
  thresholds,
  sparkline,
  colorIndex = 0,
  align = 'left',
}: StatProps) {
  const styles = getVizStyles();
  const shown = display(value, { unit, decimals, thresholds });
  const accent = shown.color ?? seriesColor(colorIndex);
  const path = sparkline ? sparklinePath(sparkline) : '';

  // Per-render values ride as custom properties so the rules stay in the
  // stylesheet, matching how TraceViewer sizes its bars.
  const vars = {
    '--viz-accent': accent,
    ...(shown.color ? { '--viz-value-color': shown.color } : {}),
  } as CSSProperties;

  return (
    <div className={styles.stat} data-align={align} style={vars}>
      {label && (
        <div className={styles.statLabel} title={label}>
          {label}
        </div>
      )}
      <div className={styles.statValue}>
        {shown.text}
        {shown.suffix && <span className={styles.statSuffix}>{shown.suffix}</span>}
      </div>
      {path && (
        <svg viewBox="0 0 100 28" preserveAspectRatio="none" aria-hidden="true" className={styles.sparkline}>
          <path d={path} fill="none" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
        </svg>
      )}
    </div>
  );
}

/** Responsive grid of stats — the N-series instant case. */
export function StatGroup({ children }: { children: React.ReactNode }) {
  const styles = getVizStyles();
  return <div className={styles.statGroup}>{children}</div>;
}
