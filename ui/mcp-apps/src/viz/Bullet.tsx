import type { CSSProperties } from 'react';

import { display, type Threshold, type Unit } from './format';
import { withAlpha } from './theme';
import { getVizStyles } from './Viz.styles';

/**
 * Bullet graph: a measure against a bounded scale, with threshold bands behind
 * it and an optional target marker.
 *
 * Replaces the radial gauge. A gauge handles exactly one value and a column of
 * dials is unreadable, whereas bullets stack and compare at a glance — which
 * matters because an instant query routinely returns many series. It is also
 * denser, and it needs no charting library: a bullet is a rectangle, some bands
 * and a tick.
 *
 * Like the gauge it replaces, this is only an honest default when the unit
 * bounds the scale (`percent`, `percentunit`) — Prometheus supplies no min/max,
 * and a bar against an invented maximum misleads. See `isBoundedUnit`.
 */

export type BulletProps = {
  value: number;
  label?: string;
  unit?: Unit;
  decimals?: number;
  /** Scale bounds. Defaults follow the unit: 0–1 for `percentunit`, else 0–100. */
  min?: number;
  max?: number;
  /** Threshold steps, drawn as qualitative bands behind the measure. */
  thresholds?: Threshold[];
  /** Comparative marker, drawn as a tick across the measure. */
  target?: number;
  /**
   * Draws the scale endpoints. In a group only the last bullet needs them —
   * every bullet shares the scale, so repeating it is noise.
   */
  showScale?: boolean;
};

/** Bounds implied by the unit, when the caller gives none. */
function impliedBounds(unit: Unit | undefined): { min: number; max: number } {
  if (unit === 'percentunit') return { min: 0, max: 1 };
  return { min: 0, max: 100 };
}

/**
 * Threshold steps as drawable bands: each covers from its own value to the next
 * step, clamped to the scale. The lowest step acts as the base.
 */
export function thresholdBands(
  thresholds: Threshold[] | undefined,
  min: number,
  max: number
): Array<{ fromPct: number; widthPct: number; color: string }> {
  if (!thresholds?.length || max <= min) return [];
  const span = max - min;
  const sorted = [...thresholds].sort((a, b) => a.value - b.value);
  const pct = (value: number) => Math.min(100, Math.max(0, ((value - min) / span) * 100));

  return sorted
    .map((step, index) => {
      const start = Number.isFinite(step.value) ? pct(step.value) : 0;
      const next = sorted[index + 1];
      const end = next ? pct(next.value) : 100;
      return { fromPct: start, widthPct: Math.max(0, end - start), color: step.color };
    })
    .filter((band) => band.widthPct > 0);
}

export function Bullet({
  value,
  label,
  unit = 'percent',
  decimals,
  min,
  max,
  thresholds,
  target,
  showScale = true,
}: BulletProps) {
  const styles = getVizStyles();
  const bounds = impliedBounds(unit);
  const lo = min ?? bounds.min;
  const hi = max ?? bounds.max;
  const span = hi - lo;

  const shown = display(value, { unit, decimals, thresholds });
  const measurePct = span > 0 ? Math.min(100, Math.max(0, ((value - lo) / span) * 100)) : 0;
  const bands = thresholdBands(thresholds, lo, hi);
  // One colour for every bullet in a group. The categorical palette would imply
  // the series differ in kind, when a bullet group compares like against like on
  // a shared scale — position already tells them apart. Semantics come from the
  // threshold colour, or from the bands behind the measure.
  const measureColor = bands.length ? 'var(--mcp-foreground)' : (shown.color ?? 'var(--mcp-primary)');

  const vars = {
    '--viz-measure': `${measurePct}%`,
    '--viz-measure-color': measureColor,
    ...(target !== undefined && span > 0
      ? { '--viz-target': `${Math.min(100, Math.max(0, ((target - lo) / span) * 100))}%` }
      : {}),
  } as CSSProperties;

  return (
    <div className={styles.bullet} style={vars}>
      <div className={styles.bulletHeader}>
        {label && (
          <span className={styles.bulletLabel} title={label}>
            {label}
          </span>
        )}
        <span className={styles.bulletValue} style={shown.color ? ({ color: shown.color } as CSSProperties) : undefined}>
          {shown.formatted}
        </span>
      </div>

      <div
        className={styles.bulletTrack}
        role="meter"
        aria-valuenow={Number.isFinite(value) ? value : undefined}
        aria-valuemin={lo}
        aria-valuemax={hi}
        aria-label={label}
        aria-valuetext={shown.formatted}
      >
        {bands.map((band) => (
          <span
            key={`${band.color}-${band.fromPct}`}
            className={styles.bulletBand}
            style={
              {
                left: `${band.fromPct}%`,
                width: `${band.widthPct}%`,
                background: withAlpha(band.color, 0.25),
              } as CSSProperties
            }
          />
        ))}
        <span className={styles.bulletMeasure} />
        {target !== undefined && <span className={styles.bulletTarget} />}
      </div>

      {showScale && (
        <div className={styles.bulletScale}>
          <span>{display(lo, { unit, decimals }).formatted}</span>
          <span>{display(hi, { unit, decimals }).formatted}</span>
        </div>
      )}
    </div>
  );
}

/** Stacked bullets — the reason this replaces the gauge. */
export function BulletGroup({ children }: { children: React.ReactNode }) {
  const styles = getVizStyles();
  return <div className={styles.bulletGroup}>{children}</div>;
}
