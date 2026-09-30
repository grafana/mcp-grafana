import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { RenderTraceApp } from '../src/trace/RenderTraceApp';
import { getTraceSummary, TraceViewer } from '../src/trace/TraceViewer';
import type { RenderTraceResult, TraceSpan } from '../src/trace/types';

afterEach(cleanup);

const exceptionEvent = {
  name: 'exception',
  timeMs: 1450,
  attributes: {
    'exception.type': 'java.net.SocketTimeoutException',
    'exception.message': 'Read timed out',
    'exception.stacktrace': 'SocketTimeoutException\n  at Gateway.authorize(Gateway.java:42)',
  },
};

const spans: TraceSpan[] = [
  {
    id: 'root',
    name: 'POST /checkout',
    serviceName: 'frontend',
    startTimeMs: 1000,
    durationMs: 1000,
    status: 'ok',
    attributes: { 'http.method': 'POST' },
    events: [],
  },
  {
    id: 'payment',
    parentId: 'root',
    name: 'authorize payment',
    serviceName: 'paymentservice',
    startTimeMs: 1200,
    durationMs: 600,
    status: 'error',
    attributes: { 'server.address': 'payments.example.test' },
    events: [exceptionEvent],
  },
  {
    id: 'inventory',
    parentId: 'root',
    name: 'reserve inventory',
    serviceName: 'inventoryservice',
    startTimeMs: 1100,
    durationMs: 200,
    status: 'ok',
    attributes: {},
    events: [],
  },
];

function result(overrides: Partial<RenderTraceResult> = {}): RenderTraceResult {
  return {
    traceId: '4bf92f3577b34da6a3ce929d0e0e4736',
    datasourceUid: 'tempo-production',
    grafanaUrl: 'https://grafana.example.test/explore?trace=4bf92f',
    spans,
    ...overrides,
  };
}

describe('TraceViewer', () => {
  it('selects the first exception, shows its ancestor path, and puts the exception before metadata', () => {
    render(<TraceViewer result={result()} />);

    expect(screen.getByRole('status').textContent).toContain('Focused path · 2 of 3 spans');
    expect(
      screen.getByRole('option', { name: 'paymentservice, authorize payment, exception' }).getAttribute('aria-selected')
    ).toBe('true');

    const details = screen.getByRole('region', { name: 'Selected span' });
    expect(within(details).getByText('java.net.SocketTimeoutException: Read timed out')).toBeTruthy();
    expect(within(details).getByLabelText('Exception stack trace').textContent).toContain('Gateway.authorize');
    const exception = within(details).getByText(/Exception event/);
    const serviceMetadata = within(details).getByText('Service');
    expect(exception.compareDocumentPosition(serviceMetadata) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('keeps the focused path anchored while changing selection until explicitly refocused', async () => {
    const user = userEvent.setup();
    render(<TraceViewer result={result()} />);

    await user.click(screen.getByRole('option', { name: 'frontend, POST /checkout' }));
    expect(screen.getByRole('status').textContent).toContain('Focused path · 2 of 3 spans');
    expect(screen.getByRole('option', { name: 'paymentservice, authorize payment, exception' })).toBeTruthy();

    await user.click(screen.getByRole('button', { name: 'Focus selected path' }));
    expect(screen.getByRole('status').textContent).toContain('Focused path · 1 of 3 spans');
  });

  it('does not classify an OK span with a handled exception as an error', async () => {
    const user = userEvent.setup();
    render(<TraceViewer result={result({ spans: spans.map((span) => ({ ...span, status: 'ok' })) })} />);

    await user.click(screen.getByRole('button', { name: 'Errors only' }));
    expect(screen.getByRole('status').textContent).toContain('0 of 3 spans');
  });

  it('selects a real error before a handled exception and reports both', () => {
    const trace = result({
      spans: [
        { ...spans[0], status: 'ok', events: [exceptionEvent] },
        { ...spans[1], status: 'error', events: [] },
      ],
    });
    render(<TraceViewer result={trace} />);

    expect(screen.getByRole('heading', { name: 'authorize payment' })).toBeTruthy();
    expect(getTraceSummary(trace).description).toContain('1 error');
    expect(getTraceSummary(trace).description).toContain('1 exception span');
  });

  it('defaults healthy traces to all spans', () => {
    render(
      <TraceViewer
        result={result({
          spans: spans.map((span) => ({ ...span, status: 'ok', events: [] })),
        })}
      />
    );

    expect(screen.getByRole('status').textContent).toContain('3 of 3 spans');
  });

  it('honors a requested focus span and filters the full trace without losing selection', async () => {
    const user = userEvent.setup();
    render(<TraceViewer result={result({ focusSpanId: 'inventory' })} />);

    expect(
      screen.getByRole('option', { name: 'inventoryservice, reserve inventory' }).getAttribute('aria-selected')
    ).toBe('true');
    await user.click(screen.getByRole('button', { name: 'Show all spans' }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search spans' }), { target: { value: 'payment' } });
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('1 of 3 spans'));
    expect(screen.getByRole('option', { name: 'paymentservice, authorize payment, exception' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'reserve inventory' })).toBeTruthy();
  });

  it('retains duplicate span IDs in the waterfall', () => {
    render(
      <TraceViewer
        result={result({
          spans: [
            { ...spans[0], id: 'duplicate', name: 'first duplicate' },
            {
              ...spans[1],
              id: 'duplicate',
              name: 'second duplicate',
              parentId: undefined,
              status: 'ok',
              events: [],
            },
          ],
        })}
      />
    );

    expect(screen.getByRole('status').textContent).toContain('2 of 2 spans');
    expect(screen.getByRole('option', { name: 'frontend, first duplicate' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'paymentservice, second duplicate' })).toBeTruthy();
  });

  it('resets the viewport when a new trace replaces a scrolled trace', async () => {
    const manySpans = Array.from({ length: 200 }, (_, index) => ({
      ...spans[0],
      id: `span-${index}`,
      name: `operation ${index}`,
      startTimeMs: index,
      events: [],
    }));
    const { rerender } = render(<TraceViewer result={result({ spans: manySpans })} />);
    const viewport = screen.getByRole('listbox', { name: 'Trace spans' });
    viewport.scrollTop = 4000;
    fireEvent.scroll(viewport);
    expect(viewport.scrollTop).toBe(4000);

    rerender(<TraceViewer result={result({ traceId: 'next-trace', spans: [spans[0]] })} />);
    await waitFor(() => expect(viewport.scrollTop).toBe(0));
    expect(screen.getByRole('option', { name: 'frontend, POST /checkout' })).toBeTruthy();
  });

  it('virtualizes a 12,000-span trace and keeps hierarchy cycles bounded', async () => {
    const user = userEvent.setup();
    const manySpans: TraceSpan[] = Array.from({ length: 12_000 }, (_, index) => ({
      id: `span-${index}`,
      parentId: index === 0 ? 'span-1' : index === 1 ? 'span-0' : 'span-0',
      name: `operation ${index}`,
      serviceName: `service-${index % 8}`,
      startTimeMs: index,
      durationMs: 10,
      status: 'ok',
      attributes: {},
      events: [],
    }));

    render(<TraceViewer result={result({ spans: manySpans, focusSpanId: 'span-0' })} />);
    await user.click(screen.getByRole('button', { name: 'Show all spans' }));
    const listbox = screen.getByRole('listbox', { name: 'Trace spans' });
    listbox.focus();
    await user.keyboard('{End}');

    expect(screen.getByRole('status').textContent).toContain('12,000 of 12,000 spans');
    expect(screen.getAllByRole('option', { name: /^service-\d, operation \d/ }).length).toBeLessThan(40);
    expect(screen.getByRole('option', { name: 'service-7, operation 11999' }).getAttribute('aria-selected')).toBe(
      'true'
    );
  });
});

describe('RenderTraceApp', () => {
  it('wraps the viewer in the real shell and lets the host intercept Grafana navigation', async () => {
    const user = userEvent.setup();
    const onOpenInGrafana = vi.fn();
    render(<RenderTraceApp result={result()} colorMode="dark" onOpenInGrafana={onOpenInGrafana} />);

    const shell = screen.getByRole('article');
    expect(shell.getAttribute('data-color-mode')).toBe('dark');
    expect(within(shell).getByText('Tempo / tempo-production')).toBeTruthy();
    await user.click(screen.getByRole('link', { name: /Open in Grafana/ }));
    expect(onOpenInGrafana).toHaveBeenCalledWith({ url: 'https://grafana.example.test/explore?trace=4bf92f' });
  });

  it('does not render unsafe Grafana navigation URLs', () => {
    render(<RenderTraceApp colorMode="light" result={result({ grafanaUrl: 'javascript:alert(1)' })} />);

    expect(screen.queryByRole('link', { name: 'Open in Grafana' })).toBeNull();
  });

  it('announces a missing requested focus span and falls back to the exception', () => {
    render(<RenderTraceApp colorMode="light" result={result({ focusSpanId: 'missing-span' })} />);

    expect(
      screen.getAllByRole('status').some((status) => status.textContent?.includes('Span missing-span was not found'))
    ).toBe(true);
    expect(screen.getByRole('heading', { name: 'authorize payment' })).toBeTruthy();
  });
});
