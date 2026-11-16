import type { App } from '@modelcontextprotocol/ext-apps';
import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { GrafanaApplication } from '../app/GrafanaApplication';
import { createAppHostState } from '../app/appHostState';
import { readVizFixture } from './vizFixtures';

const renderer = vi.hoisted(() => ({
  render: vi.fn(),
  update: vi.fn(),
  destroy: vi.fn(),
}));

vi.mock('../renderer/renderer.js', () => ({
  render: (...args: unknown[]) => {
    renderer.render(...args);
    return { update: renderer.update, destroy: renderer.destroy };
  },
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

type Handlers = { onhostcontextchanged?: (context: unknown) => void };

function view() {
  const app = {
    connect: vi.fn(async () => {}),
    close: vi.fn(async () => {}),
    getHostContext: () => undefined,
  } as unknown as App & Handlers;
  const host = createAppHostState(app);
  act(() => {
    app.ontoolinput?.({
      arguments: { toolName: 'query_datasource', arguments: readVizFixture('prometheus-line', 'args.json') },
    } as never);
    app.ontoolresult?.({ structuredContent: readVizFixture('prometheus-line', 'result.json'), content: [] } as never);
  });
  return { app, ui: render(<GrafanaApplication host={host} />) };
}

describe('RendererView', () => {
  it('renders once, updates on a new spec and destroys on unmount (renderer.2 to renderer.5)', async () => {
    const { app, ui } = view();
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    expect(renderer.render.mock.calls[0][0]).toBe(screen.getByTestId('viz-renderer'));

    act(() => app.onhostcontextchanged?.({ theme: 'dark' }));
    expect(renderer.update).toHaveBeenLastCalledWith(expect.objectContaining({ theme: 'dark' }));
    expect(renderer.render).toHaveBeenCalledTimes(1);

    ui.unmount();
    expect(renderer.destroy).toHaveBeenCalledTimes(1);
  });

  it('shows the error view when the Renderer fails', async () => {
    renderer.render.mockImplementationOnce(() => {
      throw new Error('boom');
    });
    const log = vi.spyOn(console, 'error').mockImplementation(() => {});
    view();
    expect(await screen.findByText('This view failed to render')).toBeTruthy();
    log.mockRestore();
  });

  it('draws the next result after a Renderer failure', async () => {
    renderer.render.mockImplementationOnce(() => {
      throw new Error('boom');
    });
    const log = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { app } = view();
    await screen.findByText('This view failed to render');
    act(() => {
      app.ontoolresult?.({ structuredContent: readVizFixture('prometheus-line', 'result.json'), content: [] } as never);
    });
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(2));
    expect(screen.queryByText('This view failed to render')).toBeNull();
    log.mockRestore();
  });
});
