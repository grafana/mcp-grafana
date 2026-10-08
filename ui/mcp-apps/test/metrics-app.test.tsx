import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { parsePrometheusResult } from '../app/parsePrometheusResult';
import { RenderMetricsApp } from '../src/metrics/RenderMetricsApp';
import { matrixSingleSeries, vectorSingleUnbounded } from './metricsFixtures';

// jsdom has no canvas or ResizeObserver; the chart itself is not under test here.
vi.mock('../src/viz/useEChart', () => ({
  useEChart: () => ({ containerRef: () => {}, chart: null }),
}));

afterEach(cleanup);

// A single unbounded value renders as a stat, which needs no chart engine.
const result = (exploreUrl?: string) => ({ ...parsePrometheusResult(vectorSingleUnbounded)!, exploreUrl });

describe('RenderMetricsApp', () => {
  it('offers the derived view and its alternatives, and lets the user switch', () => {
    render(<RenderMetricsApp result={result()} colorMode="light" />);

    // One compact trigger showing the current view; the options stay hidden.
    const trigger = screen.getByRole('button', { name: 'Stat' });
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByRole('listbox')).toBeNull();

    fireEvent.click(trigger);
    const options = screen.getAllByRole('option').map((option) => option.textContent);
    expect(options).toEqual(['Stat', 'Bar', 'Table']);
    expect(screen.getByRole('option', { name: 'Stat' }).getAttribute('aria-selected')).toBe('true');

    fireEvent.click(screen.getByRole('option', { name: 'Table' }));
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.getByRole('button', { name: 'Table' })).toBeTruthy();
    expect(screen.getByRole('table')).toBeTruthy();
  });

  it('closes the menu on Escape', () => {
    render(<RenderMetricsApp result={result()} colorMode="light" />);
    fireEvent.click(screen.getByRole('button', { name: 'Stat' }));
    expect(screen.getByRole('listbox')).toBeTruthy();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('keeps the diagnostic line out of the default view', () => {
    const { container, rerender } = render(<RenderMetricsApp result={result()} colorMode="light" />);
    expect(container.textContent).not.toContain('unit=');

    rerender(<RenderMetricsApp result={result()} colorMode="light" debug />);
    expect(container.textContent).toContain('unit=');
  });

  it('hands Open in Grafana to the host, and drops unsafe links', () => {
    const onOpenInGrafana = vi.fn();
    const url = 'https://example.grafana.net/explore?panes=%7B%7D';
    render(<RenderMetricsApp result={result(url)} colorMode="light" onOpenInGrafana={onOpenInGrafana} />);

    fireEvent.click(screen.getByRole('link', { name: /Open in Grafana/ }));
    expect(onOpenInGrafana).toHaveBeenCalledWith({ url });

    cleanup();
    render(<RenderMetricsApp result={result('javascript:alert(1)')} colorMode="light" />);
    expect(screen.queryByRole('link', { name: /Open in Grafana/ })).toBeNull();
  });

  it('shows a refresh failure as an error', () => {
    render(
      <RenderMetricsApp
        result={result()}
        colorMode="light"
        onRefresh={() => {}}
        notice={{ tone: 'error', message: 'The refresh query failed.' }}
      />
    );
    expect(screen.getByRole('alert').textContent).toContain('The refresh query failed.');
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy();
  });

  it('puts Refresh in the control row, between the picker and range selection', () => {
    const onRefresh = vi.fn();
    const timeseries = parsePrometheusResult(matrixSingleSeries)!;
    const { rerender } = render(
      <RenderMetricsApp result={timeseries} colorMode="light" onRefresh={onRefresh} onSelectRange={() => {}} />
    );

    const names = screen.getAllByRole('button').map((b) => b.getAttribute('aria-label') ?? b.textContent);
    expect(names).toEqual(['Time series', 'Refresh', 'Ask about a time window']);

    const refresh = screen.getByRole('button', { name: 'Refresh' });
    expect(refresh.textContent).toBe('');
    fireEvent.click(refresh);
    expect(onRefresh).toHaveBeenCalledTimes(1);

    // In flight: busy, and a second click does not queue another query.
    rerender(
      <RenderMetricsApp result={timeseries} colorMode="light" onRefresh={onRefresh} onSelectRange={() => {}} refreshing />
    );
    expect(refresh.getAttribute('aria-busy')).toBe('true');
    expect(refresh.parentElement?.dataset.tooltip).toBe('Refreshing…');
    fireEvent.click(refresh);
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it('arms range selection from an icon button and explains what to do next', () => {
    const timeseries = parsePrometheusResult(matrixSingleSeries)!;
    render(<RenderMetricsApp result={timeseries} colorMode="light" onSelectRange={() => {}} />);

    const toggle = screen.getByRole('button', { name: 'Ask about a time window' });
    expect(toggle.textContent).toBe('');
    expect(toggle.parentElement?.dataset.tooltip).toBe('Ask about a time window');
    expect(screen.queryByText(/Drag across the chart/)).toBeNull();

    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-pressed')).toBe('true');
    expect(toggle.parentElement?.dataset.tooltip).toBe('Cancel selection');
    expect(screen.getByRole('status').textContent).toContain('Drag across the chart to select a window');

    fireEvent.keyDown(document, { key: 'Escape' });
    expect(toggle.getAttribute('aria-pressed')).toBe('false');
    expect(screen.queryByText(/Drag across the chart/)).toBeNull();
  });

  it('never presents the metric name as the query', () => {
    render(<RenderMetricsApp result={result()} colorMode="light" />);
    expect(screen.getByRole('heading').textContent).toBe('Query result');
    expect(document.body.textContent).toContain('go_goroutines · 1 series · 1 point');
  });

  it('says when bars are left out instead of dropping them silently', () => {
    const many = {
      resultType: 'vector' as const,
      warnings: [],
      series: [
        ...Array.from({ length: 30 }, (_, i) => ({ labels: { route: `/r${i}` }, points: [[1, i] as [number, number]] })),
        { labels: { route: '/nan' }, points: [[1, Number.NaN] as [number, number]] },
      ],
    };
    render(<RenderMetricsApp result={many} colorMode="light" />);
    expect(document.body.textContent).toContain('Showing the top 25 of 30 series.');
    expect(document.body.textContent).toContain('1 series with no numeric value is not plotted.');
  });
});
