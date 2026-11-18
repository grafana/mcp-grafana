import type { App } from '@modelcontextprotocol/ext-apps';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
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
    getHostCapabilities: () => ({ updateModelContext: {}, ...(serverTools ? { serverTools: {} } : {}) }),
    callServerTool,
    updateModelContext: vi.fn(async () => ({})),
  } as unknown as App & Handlers;
  const host = createAppHostState(app);
  act(() => {
    app.ontoolinput?.({
      arguments: { toolName: 'query_datasource', arguments: readVizFixture('prometheus-line', 'args.json') },
    } as never);
    app.ontoolresult?.({ structuredContent: readVizFixture('prometheus-line', 'result.json'), content: [] } as never);
  });
  const context = () =>
    (app.updateModelContext as unknown as ReturnType<typeof vi.fn>).mock.calls.map(
      ([params]) => params.content[0].text as string,
    );
  return { app, context, ui: render(<GrafanaApplication host={host} />) };
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

  it('tells the model of a failed load (state.14)', async () => {
    renderer.loadFailure = 'This host cannot load the chart.';
    const { context } = view();
    await waitFor(() => expect(context()).toHaveLength(1));
    expect(context()[0]).toContain('The Grafana App could not show the timeseries chart');
    expect(context()[0]).toContain('Error: This host cannot load the chart.');
  });

  it('tells the model of a render that threw (state.14)', async () => {
    renderer.render.mockImplementationOnce(() => {
      throw new Error('boom');
    });
    const log = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { context } = view();
    await screen.findByText('This view failed to render');
    expect(context()).toEqual([expect.stringContaining('Error: boom')]);
    log.mockRestore();
  });

  it('shows and tells the model of an update that threw (state.14)', async () => {
    renderer.update.mockImplementationOnce(() => {
      throw new Error('bad spec');
    });
    const log = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { app, context } = view();
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    act(() => app.onhostcontextchanged?.({ theme: 'dark' }));
    await screen.findByText('This view failed to render');
    expect(context()).toEqual([expect.stringContaining('Error: bad spec')]);
    log.mockRestore();
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

describe('chart picker', () => {
  const result = readVizFixture('prometheus-line', 'result.json');

  it('draws the picked alternative with update, and names it in the header (spec.2)', async () => {
    view();
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    expect(screen.getByText('Time series · lgtm-prometheus')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Chart: Time series' }));
    expect(screen.getAllByRole('option').map((option) => option.textContent)).toEqual([
      'Time series',
      'Bar chart',
      'Histogram',
      'Pie chart',
      'Funnel',
      'Heatmap',
      'State timeline',
      'Status history',
      'Streamgraph',
      'Bubble',
    ]);
    expect(screen.getByRole('option', { name: 'Time series' }).getAttribute('aria-selected')).toBe('true');
    fireEvent.click(screen.getByRole('option', { name: 'Bar chart' }));

    const bar = result.alternatives.find((a: { chart: string }) => a.chart === 'barchart').panel;
    await waitFor(() =>
      expect(renderer.update).toHaveBeenLastCalledWith(
        expect.objectContaining({ panel: expect.objectContaining({ type: bar.type, options: bar.options }) }),
      ),
    );
    expect(screen.getByText('Bar chart · lgtm-prometheus')).toBeTruthy();
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('shows no picker for a result with no alternatives', async () => {
    const { app } = view();
    act(() => {
      app.ontoolresult?.({ structuredContent: { ...result, alternatives: [] }, content: [] } as never);
    });
    await waitFor(() => expect(renderer.render).toHaveBeenCalled());
    expect(screen.queryByRole('button', { name: /^Chart:/ })).toBeNull();
  });
});
