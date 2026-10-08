import { act, cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const charts: Array<{ on: ReturnType<typeof vi.fn>; dispatchAction: ReturnType<typeof vi.fn> }> = [];
vi.mock('../src/viz/echartsSetup', () => ({
  echarts: {
    init: vi.fn(() => {
      const chart = { setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn(), on: vi.fn(), off: vi.fn(), dispatchAction: vi.fn() };
      charts.push(chart);
      return chart;
    }),
  },
}));
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe() {}
    disconnect() {}
  }
);

const { TimeSeriesChart } = await import('../src/viz/TimeSeriesChart');

const series = [{ labels: { job: 'a' }, points: [[1, 1], [2, 2]] as Array<[number, number]> }];
const armedBrush = (chart: (typeof charts)[number]) =>
  chart.dispatchAction.mock.calls.some(
    ([action]) => action.type === 'takeGlobalCursor' && action.brushOption?.brushType === 'lineX'
  );

afterEach(() => {
  cleanup();
  charts.length = 0;
});

describe('TimeSeriesChart range selection', () => {
  it('binds the brush when the chart mounts with selection already armed', () => {
    // E.g. switching to another view and back while selection is on.
    render(<TimeSeriesChart series={series} selecting onSelectRange={() => {}} />);
    const chart = charts.at(-1)!;
    expect(chart.on).toHaveBeenCalledWith('brushEnd', expect.any(Function));
    expect(armedBrush(chart)).toBe(true);
  });

  it('rebinds on the new instance when a theme change re-creates the chart', async () => {
    // A stable callback, so only the new chart instance can re-run the binding.
    const onSelectRange = () => {};
    const { rerender } = render(<TimeSeriesChart series={series} selecting onSelectRange={onSelectRange} />);
    await act(async () =>
      rerender(<TimeSeriesChart series={series} selecting onSelectRange={onSelectRange} colorMode="dark" />)
    );
    expect(charts).toHaveLength(2);
    expect(charts[1].on).toHaveBeenCalledWith('brushEnd', expect.any(Function));
    expect(armedBrush(charts[1])).toBe(true);
  });
});
