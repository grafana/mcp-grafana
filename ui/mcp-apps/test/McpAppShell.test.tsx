import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { McpAppSection, McpAppShell } from '../src';

afterEach(cleanup);

const summary = { label: 'Summary of proposal', title: 'Review checkout latency', description: 'Keep useful context.' };

describe('McpAppShell', () => {
  it.each(['success', 'info'] as const)('announces only the %s message, excluding its action', (tone) => {
    render(
      <McpAppShell
        colorMode="light"
        product="Grafana"
        summary={summary}
        feedback={{ tone, message: 'Proposal saved.', action: { label: 'View proposal', onClick: vi.fn() } }}
      />
    );
    expect(screen.getByRole('status').textContent).toBe('Proposal saved.');
    expect(screen.getByRole('status').contains(screen.getByRole('button', { name: 'View proposal' }))).toBe(false);
  });

  it('does not invent controls when the app has not supplied actions', () => {
    render(<McpAppShell colorMode="light" product="Grafana" summary={summary} />);
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.queryByRole('link')).toBeNull();
    expect(screen.getByText('Keep useful context.')).toBeTruthy();
  });

  it('supports independent app roots and live mode changes without mutating the host document', () => {
    document.documentElement.setAttribute('data-color-mode', 'light');
    const { rerender } = render(<McpAppShell colorMode="dark" product="Grafana" summary={summary} />);
    expect(screen.getByRole('article').getAttribute('data-color-mode')).toBe('dark');
    expect(document.documentElement.getAttribute('data-color-mode')).toBe('light');
    rerender(<McpAppShell colorMode="light" product="Grafana" summary={summary} />);
    expect(screen.getByRole('article').getAttribute('data-color-mode')).toBe('light');
    document.documentElement.removeAttribute('data-color-mode');
  });

  it('supports keyboard actions and prevents submission while pending', async () => {
    const onClick = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <McpAppShell colorMode="light" product="Grafana" summary={summary} primaryAction={{ label: 'Apply', onClick }} />
    );
    await user.tab();
    await user.keyboard('{Enter}');
    expect(onClick).toHaveBeenCalledTimes(1);
    rerender(
      <McpAppShell
        colorMode="light"
        product="Grafana"
        summary={summary}
        primaryAction={{ label: 'Apply', onClick, pending: true }}
      />
    );
    const button = screen.getByRole('button', { name: /Apply/ });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('lets the host intercept navigation while preserving a real link', () => {
    const onClick = vi.fn((event) => event.preventDefault());
    render(
      <McpAppShell
        colorMode="light"
        product="Grafana"
        summary={summary}
        openInGrafana={{ href: 'https://example.grafana.net/d/checkout', onClick }}
      />
    );
    const link = screen.getByRole('link', { name: /Open in Grafana/ });
    expect(link.getAttribute('href')).toBe('https://example.grafana.net/d/checkout');
    fireEvent.click(link);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('preserves interactive content and offers recovery alongside an announced error', async () => {
    const retry = vi.fn();
    const user = userEvent.setup();
    render(
      <McpAppShell
        colorMode="light"
        product="Grafana"
        summary={summary}
        feedback={{
          tone: 'error',
          message: 'Could not save the proposal.',
          action: { label: 'Try again', onClick: retry },
        }}
      >
        <McpAppSection title="Threshold" result="Current threshold: 500 ms">
          <label>
            Threshold <input defaultValue="500" />
          </label>
        </McpAppSection>
      </McpAppShell>
    );
    expect(screen.getByRole('alert').textContent).toBe('Could not save the proposal.');
    expect(screen.getByRole('alert').contains(screen.getByRole('button', { name: 'Try again' }))).toBe(false);
    expect((screen.getByRole('textbox', { name: 'Threshold' }) as HTMLInputElement).value).toBe('500');
    await user.tab();
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Try again' }));
    await user.keyboard('{Enter}');
    expect(retry).toHaveBeenCalledTimes(1);
  });
});
