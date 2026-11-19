import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { COPY_RESULT_MS, QueryBlock } from '../src/QueryBlock';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const queries = [
  { refId: 'A', text: 'sum by (handler) (\n  rate(http_requests_total[5m])\n)' },
  { refId: 'B', text: 'up' },
];

/** Stubs the Clipboard API with `writeText`, and document.execCommand, which jsdom does not have. */
function stubClipboard(writeText: (text: string) => Promise<void>, execCommand = vi.fn(() => false)) {
  vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText: vi.fn(writeText) } });
  Object.defineProperty(document, 'execCommand', { value: execCommand, configurable: true });
  return { writeText: navigator.clipboard.writeText, execCommand };
}

describe('QueryBlock', () => {
  it('shows the queries on one line, and each as written when expanded', () => {
    render(<QueryBlock datasource="prometheus" queries={queries} />);
    const block = screen.getByRole('region', { name: 'Query' });
    expect(block.textContent).toContain('prometheus');
    expect(block.textContent).toContain('sum by (handler) ( rate(http_requests_total[5m]) )  ·  up');
    expect(block.querySelector('pre')).toBeNull();

    const toggle = screen.getByRole('button', { name: 'Expand query' });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(toggle);
    expect(screen.getByRole('button', { name: 'Collapse query' }).getAttribute('aria-expanded')).toBe('true');
    expect([...block.querySelectorAll('pre')].map((pre) => pre.textContent)).toEqual([
      'sum by (handler) (\n  rate(http_requests_total[5m])\n)',
      'up',
    ]);
    expect(screen.getByText('A')).toBeTruthy();
    expect(screen.getByText('B')).toBeTruthy();
  });

  it('shows no refId for a single query', () => {
    render(<QueryBlock datasource="prometheus" queries={[{ refId: 'A', text: 'up' }]} />);
    fireEvent.click(screen.getByRole('button', { name: 'Expand query' }));
    expect(screen.queryByText('A')).toBeNull();
  });

  it('shows each query when refIds are repeated or missing', () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
    render(
      <QueryBlock
        datasource="prometheus"
        queries={[
          { refId: 'A', text: 'up' },
          { refId: 'A', text: 'down' },
          { refId: '', text: 'sideways' },
        ]}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Expand query' }));
    expect([...document.querySelectorAll('pre')].map((pre) => pre.textContent)).toEqual(['up', 'down', 'sideways']);
    expect(screen.getAllByText('A')).toHaveLength(2);
    expect(errors).not.toHaveBeenCalled();
  });

  it('copies all queries, then shows Copy again', async () => {
    vi.useFakeTimers();
    const { writeText } = stubClipboard(() => Promise.resolve());
    render(<QueryBlock datasource="prometheus" queries={queries} />);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Copy query' })));

    expect(writeText).toHaveBeenCalledWith('sum by (handler) (\n  rate(http_requests_total[5m])\n)\n\nup');
    expect(screen.getByRole('button', { name: 'Query copied' })).toBeTruthy();
    expect(screen.getByRole('status').textContent).toBe('Query copied');
    act(() => vi.advanceTimersByTime(COPY_RESULT_MS));
    expect(screen.getByRole('button', { name: 'Copy query' })).toBeTruthy();
    expect(screen.getByRole('status').textContent).toBe('');
  });

  it('copies through a text area when the host blocks the Clipboard API', async () => {
    const { execCommand } = stubClipboard(() => Promise.reject(new Error('blocked')), vi.fn(() => true));
    render(<QueryBlock datasource="prometheus" queries={[{ refId: 'A', text: 'up' }]} />);
    const copy = screen.getByRole('button', { name: 'Copy query' });
    copy.focus();
    await act(async () => fireEvent.click(copy));

    expect(execCommand).toHaveBeenCalledWith('copy');
    expect(screen.getByRole('button', { name: 'Query copied' })).toBe(document.activeElement);
    expect(document.querySelector('textarea')).toBeNull();
  });

  it('says so when it cannot copy', async () => {
    stubClipboard(() => Promise.reject(new Error('blocked')));
    render(<QueryBlock datasource="prometheus" queries={[{ refId: 'A', text: 'up' }]} />);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Copy query' })));
    expect(screen.getByRole('status').textContent).toBe('Could not copy the query');
  });
});
