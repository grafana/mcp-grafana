import type { App } from '@modelcontextprotocol/ext-apps';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { VIZ_RENDERER_GLOBAL } from '../vizRenderer.config';

const URI = 'ui://grafana/viz-renderer-test.js';

// jsdom runs inline scripts in a window that the test cannot see, so the test runs the script itself: each text
// names what the script does. Real execution is checked against the built files in a browser.
const behaviors: Record<string, () => void> = {
  'set the global': () => {
    (window as unknown as Record<string, unknown>)[VIZ_RENDERER_GLOBAL] = { render: () => 'drawn' };
  },
  throw: () => {
    window.dispatchEvent(new ErrorEvent('error', { error: new Error('bad build'), message: 'bad build' }));
  },
  'blocked by CSP': () => {
    setTimeout(() => document.dispatchEvent(new Event('securitypolicyviolation')), 0);
  },
  'do nothing': () => {},
};

type Contents = { uri: string; text?: string; blob?: string };

function fakeApp(contents: Contents[] | Error = [{ uri: URI, text: 'set the global' }], serverResources = true) {
  const readServerResource = vi.fn(async () => {
    if (contents instanceof Error) throw contents;
    return { contents };
  });
  const app = {
    getHostCapabilities: () => (serverResources ? { serverResources: {} } : {}),
    readServerResource,
  } as unknown as App;
  return { app, readServerResource };
}

// A fresh module for each test, so no loaded Renderer carries over.
const importLoader = () => import('../app/loadRenderer');

beforeEach(() => {
  vi.resetModules();
  const append = document.head.appendChild.bind(document.head);
  vi.spyOn(document.head, 'appendChild').mockImplementation(<T extends Node>(node: T) => {
    const behavior = node instanceof HTMLScriptElement ? behaviors[node.textContent ?? ''] : undefined;
    if (!behavior) return append(node);
    behavior();
    return node;
  });
});

afterEach(() => {
  delete (window as unknown as Record<string, unknown>)[VIZ_RENDERER_GLOBAL];
});

describe('loadRenderer', () => {
  it('reads the Renderer resource and runs it as an inline script', async () => {
    const { loadRenderer, VIZ_RENDERER_URI } = await importLoader();
    const { app, readServerResource } = fakeApp();
    const module = await loadRenderer(app);

    expect(VIZ_RENDERER_URI).toBe(URI);
    expect(readServerResource).toHaveBeenCalledWith({ uri: URI });
    const [script] = vi.mocked(document.head.appendChild).mock.calls[0] as [HTMLScriptElement];
    expect(script.src).toBe('');
    expect((module.render as unknown as () => string)()).toBe('drawn');
  });

  it('reads the resource once per iframe', async () => {
    const { loadRenderer } = await importLoader();
    const { app, readServerResource } = fakeApp();
    await loadRenderer(app);
    await loadRenderer(app);

    expect(readServerResource).toHaveBeenCalledTimes(1);
  });

  it('runs a resource that comes back as a base64 blob', async () => {
    const { loadRenderer } = await importLoader();
    await expect(loadRenderer(fakeApp([{ uri: URI, blob: btoa('set the global') }]).app)).resolves.toBeTruthy();
  });

  it('fails without a read when the host does not pass resource reads to the server', async () => {
    const { loadRenderer } = await importLoader();
    const { app, readServerResource } = fakeApp(undefined, false);

    await expect(loadRenderer(app)).rejects.toThrow('does not pass resource reads to the server');
    expect(readServerResource).not.toHaveBeenCalled();
  });

  it('runs only the resource with the Renderer URI', async () => {
    const { loadRenderer } = await importLoader();
    const app = fakeApp([{ uri: 'ui://grafana/app.html', text: 'set the global' }]).app;

    await expect(loadRenderer(app)).rejects.toThrow('Could not read the chart script: the host returned another resource');
    expect(document.head.appendChild).not.toHaveBeenCalled();
  });

  it('fails on an empty resource', async () => {
    const { loadRenderer } = await importLoader();
    await expect(loadRenderer(fakeApp([{ uri: URI, text: '' }]).app)).rejects.toThrow('it came back empty');
  });

  it('reads again on the next call after a failed read', async () => {
    const { loadRenderer } = await importLoader();
    await expect(loadRenderer(fakeApp(new Error('Request timed out.')).app)).rejects.toThrow(
      'Could not read the chart script: Request timed out',
    );

    await expect(loadRenderer(fakeApp().app)).resolves.toBeTruthy();
  });

  it('says the server was updated when it does not have the Renderer of this App', async () => {
    const { loadRenderer } = await importLoader();
    const notFound = Object.assign(new Error('MCP error -32602: Resource not found'), { code: -32602 });
    await expect(loadRenderer(fakeApp(notFound).app)).rejects.toThrow(
      'The chart script changed because the server was updated. Ask again to redraw the chart.',
    );
  });

  it('does not take another missing thing for an updated server', async () => {
    const { loadRenderer } = await importLoader();
    await expect(loadRenderer(fakeApp(new Error('session not found')).app)).rejects.toThrow(
      'Could not read the chart script: session not found',
    );
  });

  it('keeps the failure of a script that throws, and does not read again', async () => {
    const { loadRenderer } = await importLoader();
    await expect(loadRenderer(fakeApp([{ uri: URI, text: 'throw' }]).app)).rejects.toThrow(
      'The chart script failed: bad build',
    );

    const { app, readServerResource } = fakeApp();
    await expect(loadRenderer(app)).rejects.toThrow('bad build');
    expect(readServerResource).not.toHaveBeenCalled();
  });

  it('says when the content security policy of the host blocks the script', async () => {
    const { loadRenderer } = await importLoader();
    await expect(loadRenderer(fakeApp([{ uri: URI, text: 'blocked by CSP' }]).app)).rejects.toThrow(
      'The content security policy of the host blocked the chart script.',
    );
  });

  it('fails when the script does not set the global', async () => {
    const { loadRenderer } = await importLoader();
    await expect(loadRenderer(fakeApp([{ uri: URI, text: 'do nothing' }]).app)).rejects.toThrow('did not start');
  });
});
