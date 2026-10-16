import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const init = vi.fn(() => ({ setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn() }));
vi.mock('../src/viz/echartsSetup', () => ({ echarts: { init } }));
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe() {}
    disconnect() {}
  }
);

const { useEChart } = await import('../src/viz/useEChart');

function Chart({ empty }: { empty: boolean }) {
  const { containerRef } = useEChart(() => ({}), 'light', [empty]);
  // Like Heatmap: an empty state replaces the chart div.
  return empty ? <div>Nothing to plot.</div> : <div ref={containerRef} />;
}

afterEach(() => {
  cleanup();
  init.mockClear();
});

describe('useEChart', () => {
  it('creates the chart when its container appears after an empty state', () => {
    const { rerender } = render(<Chart empty />);
    expect(init).not.toHaveBeenCalled();

    rerender(<Chart empty={false} />);
    expect(init).toHaveBeenCalledTimes(1);
  });

  it('disposes the chart when the container goes away', () => {
    const { rerender } = render(<Chart empty={false} />);
    const chart = init.mock.results[0].value;
    rerender(<Chart empty />);
    expect(chart.dispose).toHaveBeenCalled();
  });
});
