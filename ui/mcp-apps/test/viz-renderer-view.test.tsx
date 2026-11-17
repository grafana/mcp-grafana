import type { App } from '@modelcontextprotocol/ext-apps';
import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { GrafanaApplication } from '../app/GrafanaApplication';
import { createAppHostState } from '../app/appHostState';
import { readVizFixture } from './vizFixtures';

const renderer = vi.hoisted(() => ({
  render: vi.fn(),
  update: vi.fn(),
  setRefreshed: vi.fn(),
  destroy: vi.fn(),
  // The message of the next failed load, if set.
  loadFailure: undefined as string | undefined,
}));

vi.mock('../app/loadRenderer', () => ({
  loadRenderer: async () => {
    const failure = renderer.loadFailure;
    renderer.loadFailure = undefined;
    if (failure) throw new Error(failure);
    return {
      render: (...args: unknown[]) => {
        renderer.render(...args);
        return { update: renderer.update, setRefreshed: renderer.setRefreshed, destroy: renderer.destroy };
      },
    };
  },
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  renderer.loadFailure = undefined;
});

type Handlers = { onhostcontextchanged?: (context: unknown) => void };

function view(callServerTool = vi.fn(), serverTools = true) {
  const app = {
    connect: vi.fn(async () => {}),
    close: vi.fn(async () => {}),
    getHostContext: () => undefined,
    getHostCapabilities: () => (serverTools ? { serverTools: {} } : {}),
    callServerTool,
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

  it('shows a failed load in the banner', async () => {
    renderer.loadFailure = 'This host cannot load the chart.';
    view();
    expect(await screen.findByText('This host cannot load the chart.')).toBeTruthy();
    expect(screen.queryByTestId('viz-renderer')).toBeNull();
    expect(renderer.render).not.toHaveBeenCalled();
    // Open in Grafana gives a blank page in Claude Desktop, so the viz view has none.
    expect(screen.queryByText('Open in Grafana')).toBeNull();
  });

  it('loads again for the next result after a failed load', async () => {
    renderer.loadFailure = 'The chart script came back empty.';
    const { app } = view();
    await screen.findByText(/The chart script came back empty\./);
    act(() => {
      app.ontoolresult?.({ structuredContent: readVizFixture('prometheus-line', 'result.json'), content: [] } as never);
    });
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    expect(screen.queryByText(/The chart script came back empty\./)).toBeNull();
  });
});

describe('RendererView refresh', () => {
  type Options = { onRefresh: (request: { id: string; cause: 'refresh' }) => void };
  const onRefresh = () => (renderer.render.mock.calls[0][2] as Options).onRefresh;

  it('draws the result of a refresh with update (refresh.12)', async () => {
    const result = { ...readVizFixture('prometheus-line', 'result.json'), chart: 'barchart' };
    view(vi.fn(async () => ({ structuredContent: result, content: [] })));
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));

    act(() => onRefresh()({ id: 'r1', cause: 'refresh' }));
    await waitFor(() => expect(renderer.update).toHaveBeenCalledWith(expect.objectContaining({ data: result.data })));
    expect(renderer.setRefreshed).not.toHaveBeenCalled();
  });

  it('gives no onRefresh when the host does not pass tool calls to the server (refresh.1)', async () => {
    view(vi.fn(), false);
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    expect(renderer.render.mock.calls[0][2]).toEqual({ onState: expect.any(Function) });
  });

  it('gives a failed refresh to setRefreshed (refresh.11)', async () => {
    view(vi.fn(async () => ({ isError: true, content: [{ type: 'text', text: 'boom' }] })));
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));

    act(() => onRefresh()({ id: 'r1', cause: 'refresh' }));
    await waitFor(() => expect(renderer.setRefreshed).toHaveBeenCalledWith('r1', false, 'boom'));
    expect(renderer.update).not.toHaveBeenCalled();
  });
});
