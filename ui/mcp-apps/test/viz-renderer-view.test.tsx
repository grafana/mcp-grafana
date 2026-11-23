import type { App } from '@modelcontextprotocol/ext-apps';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { GrafanaApplication } from '../app/GrafanaApplication';
import { createAppHostState } from '../app/appHostState';
import { vizMessageText } from '../app/vizMessage';
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

type FakeHost = { message?: object; sendMessage?: ReturnType<typeof vi.fn> };

function view(callServerTool = vi.fn(), serverTools = true, withResult = true, fake: FakeHost = {}) {
  const app = {
    connect: vi.fn(async () => {}),
    close: vi.fn(async () => {}),
    getHostContext: () => undefined,
    getHostCapabilities: () => ({
      updateModelContext: {},
      ...(serverTools ? { serverTools: {} } : {}),
      ...(fake.message ? { message: fake.message } : {}),
    }),
    callServerTool,
    sendMessage: fake.sendMessage ?? vi.fn(async () => ({})),
    updateModelContext: vi.fn(async () => ({})),
  } as unknown as App & Handlers;
  const host = createAppHostState(app);
  act(() => {
    app.ontoolinput?.({
      arguments: { toolName: 'query_datasource', arguments: readVizFixture('prometheus-line', 'args.json') },
    } as never);
    if (withResult) {
      app.ontoolresult?.({ structuredContent: readVizFixture('prometheus-line', 'result.json'), content: [] } as never);
    }
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
  type Request = { id: string; cause: 'refresh' | 'timeRange'; from?: string; to?: string };
  type Options = { onRefresh: (request: Request) => void };
  const onRefresh = () => (renderer.render.mock.calls[0][2] as Options).onRefresh;

  it('draws the result of a refresh with update (refresh.12)', async () => {
    const result = { ...readVizFixture('prometheus-line', 'result.json'), chart: 'barchart' };
    view(vi.fn(async () => ({ structuredContent: result, content: [] })));
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));

    act(() => onRefresh()({ id: 'r1', cause: 'refresh' }));
    await waitFor(() => expect(renderer.update).toHaveBeenCalledWith(expect.objectContaining({ data: result.data })));
    expect(renderer.setRefreshed).not.toHaveBeenCalled();
  });

  it('queries the range of a zoom and gives it as the timeRange of the new spec (refresh.9, spec.17)', async () => {
    const result = readVizFixture('prometheus-line', 'result.json');
    const call = vi.fn(async () => ({ structuredContent: result, content: [] }));
    view(call);
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    expect(renderer.render.mock.calls[0][1].timeRange).toEqual({ from: 'now-15m', to: 'now' });

    // A zoom gives epoch milliseconds as decimal strings (renderer.43).
    act(() => onRefresh()({ id: 'z1', cause: 'timeRange', from: '1790352877294', to: '1790353177294' }));
    await waitFor(() =>
      expect(renderer.update).toHaveBeenCalledWith(
        expect.objectContaining({ timeRange: { from: '1790352877294', to: '1790353177294' } }),
      ),
    );
    expect(call).toHaveBeenCalledWith(
      expect.objectContaining({
        arguments: expect.objectContaining({
          arguments: expect.objectContaining({ from: '1790352877294', to: '1790353177294' }),
        }),
      }),
    );
  });

  it('gives no onRefresh when the host does not pass tool calls to the server (refresh.1)', async () => {
    view(vi.fn(), false);
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    expect(renderer.render.mock.calls[0][2]).not.toHaveProperty('onRefresh');
  });

  it('gives a failed refresh to setRefreshed (refresh.11)', async () => {
    view(vi.fn(async () => ({ isError: true, content: [{ type: 'text', text: 'boom' }] })));
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));

    act(() => onRefresh()({ id: 'r1', cause: 'refresh' }));
    await waitFor(() => expect(renderer.setRefreshed).toHaveBeenCalledWith('r1', false, 'boom'));
    expect(renderer.update).not.toHaveBeenCalled();
  });
});

describe('Ask in chat', () => {
  type Request = Parameters<typeof vizMessageText>[1];
  const request: Request = readVizFixture('hostile-message', 'message.json').cases[3].request;
  // A case whose text is too long even with no labels (state.25).
  const tooLong: Request = readVizFixture('hostile-message', 'message.json').cases.find(
    (c: { text: string | null }) => c.text === null,
  ).request;
  const options = () => renderer.render.mock.calls[0][2] as { onMessage?: (request: Request) => void };
  const textBox = () => screen.queryByRole<HTMLTextAreaElement>('textbox', { name: 'Message text' });

  async function ask(answer: () => Promise<unknown>, message?: object) {
    const sendMessage = vi.fn(answer);
    const { app } = view(vi.fn(), true, true, { sendMessage, message });
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    const text = vizMessageText(renderer.render.mock.calls[0][1], request);
    return { app, sendMessage, text, onMessage: options().onMessage! };
  }

  it('sends the text of the point as a user message, with no model context (state.21, state.22)', async () => {
    const { app, sendMessage, text, onMessage } = await ask(async () => ({}));
    act(() => onMessage(request));
    expect(sendMessage).toHaveBeenCalledExactlyOnceWith({ role: 'user', content: [{ type: 'text', text }] });
    // prometheus-line has no title, so the text names the panel type (state.22).
    expect(text).toMatch(
      /^Explain the value "10 req\/s" of "plain" \{\} \(query "A"\) at the time .* in the chart "grafana-echartscartesian-panel"/,
    );
    expect(app.updateModelContext).not.toHaveBeenCalled();
  });

  it('gives onMessage unless the host takes messages with no text (state.20)', async () => {
    view(vi.fn(), true, true, { message: { image: {} } });
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    expect(options()).not.toHaveProperty('onMessage');
    cleanup();
    renderer.render.mockClear();

    view(vi.fn(), true, true, { message: { text: {} } });
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    expect(options().onMessage).toEqual(expect.any(Function));
  });

  it('shows nothing when the host takes the message (state.27)', async () => {
    const { onMessage } = await ask(async () => ({}));
    act(() => onMessage(request));
    await waitFor(() => expect(screen.queryByText('Sending to the chat…')).toBeNull());
    expect(screen.queryByRole('alert')).toBeNull();
    expect(textBox()).toBeNull();
  });

  it.each([
    ['isError true', async () => ({ isError: true })],
    ['a JSON-RPC error', async () => Promise.reject(new Error('Method not found'))],
  ])('shows an error and the selected text to copy for %s, until Close (state.27)', async (_, answer) => {
    const { sendMessage, text, onMessage } = await ask(answer);
    act(() => onMessage(request));
    const box = await screen.findByRole<HTMLTextAreaElement>('textbox', { name: 'Message text' });
    expect(screen.getByRole('alert').textContent).toContain('The chat did not take the message.');
    expect(box.value).toBe(text);
    expect(box.readOnly).toBe(true);
    expect([box.selectionStart, box.selectionEnd]).toEqual([0, text!.length]);

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('alert')).toBeNull();
    expect(textBox()).toBeNull();
    expect(sendMessage).toHaveBeenCalledTimes(1);
  });

  it('closes the text box on the next Ask in chat (state.27)', async () => {
    const { sendMessage, onMessage } = await ask(async () => ({ isError: true }));
    act(() => onMessage(request));
    await waitFor(() => expect(textBox()).not.toBeNull());
    sendMessage.mockImplementation(() => new Promise(() => {}));
    act(() => onMessage(request));
    expect(textBox()).toBeNull();
    expect(screen.getByText('Sending to the chat…')).toBeTruthy();
  });

  it('shows that it sends, and ignores Ask in chat until the host answers (state.26)', async () => {
    let resolve!: (value: unknown) => void;
    const { sendMessage, onMessage } = await ask(() => new Promise((r) => (resolve = r)));
    act(() => onMessage(request));
    expect(screen.getByText('Sending to the chat…')).toBeTruthy();
    act(() => onMessage(request));
    expect(sendMessage).toHaveBeenCalledTimes(1);

    await act(async () => resolve({}));
    expect(screen.queryByText('Sending to the chat…')).toBeNull();
  });

  it('stops the wait with no error after messageTimeoutMs, and ignores a late answer (state.26, state.27)', async () => {
    let resolve!: (value: unknown) => void;
    const { sendMessage, onMessage } = await ask(() => new Promise((r) => (resolve = r)));
    vi.useFakeTimers();
    try {
      act(() => onMessage(request));
      act(() => vi.advanceTimersByTime(60_000));
    } finally {
      vi.useRealTimers();
    }
    expect(screen.queryByText('Sending to the chat…')).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();

    sendMessage.mockImplementation(() => new Promise(() => {}));
    act(() => onMessage(request));
    expect(sendMessage).toHaveBeenCalledTimes(2);
    await act(async () => resolve({ isError: true }));
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByText('Sending to the chat…')).toBeTruthy();
  });

  it('sends nothing and shows an error with no text box for a text that is too long (state.25)', async () => {
    // Quotes double in JSON, so a long title of quotes makes the text too long even with no labels.
    const result = readVizFixture('prometheus-line', 'result.json');
    const sendMessage = vi.fn(async () => ({}));
    const { app } = view(vi.fn(), true, false, { sendMessage });
    act(() => {
      app.ontoolresult?.({
        structuredContent: { ...result, panel: { ...result.panel, title: '"'.repeat(200) } },
        content: [],
      } as never);
    });
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));
    const onMessage = (renderer.render.mock.calls[0][2] as ReturnType<typeof options>).onMessage!;
    act(() => onMessage(tooLong));
    expect(sendMessage).not.toHaveBeenCalled();
    expect(screen.getByRole('alert').textContent).toContain('too much text');
    expect(textBox()).toBeNull();
  });
});

describe('chart picker', () => {
  const result = readVizFixture('prometheus-line', 'result.json');

  it('draws the picked alternative with update, and names it on the picker (spec.2)', async () => {
    view();
    await waitFor(() => expect(renderer.render).toHaveBeenCalledTimes(1));

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
    expect(screen.getByRole('button', { name: 'Chart: Bar chart' })).toBeTruthy();
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

describe('query block', () => {
  const expr = 'sum by (handler) (rate(grafana_http_request_duration_seconds_count[5m]))';

  it('leads the view with the query of the arguments, and no summary card', async () => {
    view();
    await waitFor(() => expect(renderer.render).toHaveBeenCalled());
    const block = screen.getByRole('region', { name: 'Query' });
    expect(block.textContent).toBe(`lgtm-prometheus${expr}`);
    expect(screen.getByRole('article', { name: 'Grafana' })).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Query result' })).toBeNull();
  });

  it('gives the range and the series of the result under the expanded query', async () => {
    view();
    await waitFor(() => expect(renderer.render).toHaveBeenCalled());
    fireEvent.click(screen.getByRole('button', { name: 'Expand query' }));
    expect(screen.getByText('Last 15 minutes · 17 series')).toBeTruthy();
  });

  it('shows the query while the result is on its way', () => {
    view(vi.fn(), true, false);
    expect(screen.getByRole('region', { name: 'Query' }).textContent).toContain(expr);
    expect(screen.getByText('Waiting for query results…')).toBeTruthy();
    expect(renderer.render).not.toHaveBeenCalled();
  });
});
