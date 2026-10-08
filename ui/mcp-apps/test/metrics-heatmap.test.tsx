import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { EChartsOption } from '../src/viz/echartsSetup';

// Capture what the heatmap hands ECharts; jsdom has no canvas to draw it.
const options: EChartsOption[] = [];
vi.mock('../src/viz/useEChart', () => ({
  useEChart: (build: (colors: Record<string, string>) => EChartsOption) => {
    options.push(build({ foreground: '#000', muted: '#666' }));
    return { containerRef: () => {}, chartRef: { current: null } };
  },
}));

const { Heatmap } = await import('../src/viz/Heatmap');

afterEach(() => {
  cleanup();
  options.length = 0;
});

describe('Heatmap tooltip', () => {
  it('escapes label values, which ECharts inserts as HTML', () => {
    const series = Array.from({ length: 2 }, (_, i) => ({
      labels: { pod: i ? '<img src=x onerror=alert(1)>' : 'safe' },
      points: [[1_760_000_000_000, 1] as [number, number]],
    }));
    render(<Heatmap series={series} />);

    const tooltip = options.at(-1)!.tooltip as { formatter: (params: unknown) => string };
    const html = tooltip.formatter({ value: [0, 1, 1] });
    expect(html).not.toContain('<img');
    expect(html).toContain('&#60;img');
  });
});
