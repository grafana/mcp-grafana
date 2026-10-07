import type { MetricPoint } from './types';

/**
 * Build an SVG polyline path over a `width`×`height` viewBox.
 *
 * Lives in its own module (not inside `Stat.tsx`) so it is testable without a
 * JSX toolchain. Flat series render at mid-height rather than dividing by zero.
 */
export function sparklinePath(points: MetricPoint[], width = 100, height = 28): string {
  const values = points.map(([, value]) => value).filter((value) => Number.isFinite(value));
  if (values.length < 2) return '';
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min;
  const stepX = width / (values.length - 1);
  // Inset 1px top and bottom so a 1.5px stroke is not clipped at the extremes.
  const usable = height - 2;
  return values
    .map((value, index) => {
      const x = index * stepX;
      const y = span === 0 ? height / 2 : 1 + usable - ((value - min) / span) * usable;
      return `${index === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(' ');
}
