/**
 * Minimal MCP App host, for debugging an app outside a real host.
 *
 * Claude Desktop refuses to start with a debugging switch, so an app that
 * crashes inside its iframe there is unobservable. This page is a real host —
 * `AppBridge` over `PostMessageTransport`, the same handshake — so the app runs
 * in an iframe with a reachable console and the error can be read.
 *
 * Load with `?app=metrics.html` (default) and open the console, or drive it
 * headlessly and read the captured log from `window.__hostLog`.
 */

import { AppBridge } from '@modelcontextprotocol/ext-apps/app-bridge';
import { PostMessageTransport } from '@modelcontextprotocol/ext-apps';

const log: string[] = [];
(window as unknown as { __hostLog: string[] }).__hostLog = log;

function record(line: string) {
  log.push(line);
  // eslint-disable-next-line no-console
  console.log(`[host] ${line}`);
  const pre = document.getElementById('log');
  if (pre) pre.textContent = log.join('\n');
}

/**
 * A realistic `query_prometheus` result: a single range series, in the exact
 * wire shape `model.Value` marshals to (no `resultType`, float seconds,
 * string values), delivered on all three channels the way the Go tool does.
 */
let refreshes = 0;

/**
 * Instant-query payloads, in the same wire shape. `?fixture=` selects one, so
 * every derivation branch can be inspected without a Grafana.
 */
function instantResult(kind: string) {
  const at = Math.floor(Date.now() / 1000);
  const entries: Record<string, Array<[Record<string, string>, string]>> = {
    // Bounded unit, one value → bullet.
    bounded1: [[{ __name__: 'node_memory_utilisation_ratio', instance: 'MacBookPro.mynet' }, '0.87']],
    // Bounded unit, several values → stacked bullets against a shared scale.
    boundedN: [
      [{ __name__: 'node_filesystem_used_ratio', mountpoint: '/' }, '0.42'],
      [{ __name__: 'node_filesystem_used_ratio', mountpoint: '/System/Volumes/Data' }, '0.91'],
      [{ __name__: 'node_filesystem_used_ratio', mountpoint: '/private/var/vm' }, '0.12'],
      [{ __name__: 'node_filesystem_used_ratio', mountpoint: '/Volumes/backup' }, '0.68'],
    ],
    // Unbounded, several values → ranked bars.
    instantN: [
      [{ __name__: 'http_requests_total', route: '/api/dashboards' }, '1842'],
      [{ __name__: 'http_requests_total', route: '/api/datasources' }, '934'],
      [{ __name__: 'http_requests_total', route: '/api/search' }, '712'],
    ],
    // Unbounded, one value → stat.
    single: [[{ __name__: 'go_goroutines', job: 'grafana' }, '1403']],
  };
  const payload = {
    data: (entries[kind] ?? entries.bounded1).map(([metric, value]) => ({ metric, value: [at, value] })),
    exploreUrl: 'https://example.grafana.net/explore?panes=%7B%7D',
  };
  return {
    content: [{ type: 'text' as const, text: JSON.stringify(payload) }],
    structuredContent: payload,
    isError: false,
    _meta: { ui: { resourceUri: 'ui://grafana/metrics.html' } },
  };
}

/** Cumulative latency buckets over time → heatmap. */
function bucketResult() {
  const start = Math.floor(Date.now() / 1000) - 3600;
  const bounds = ['0.005', '0.01', '0.025', '0.05', '0.1', '0.25', '0.5', '1', '2.5', '+Inf'];
  const data = bounds.map((le, bucketIndex) => ({
    metric: { __name__: 'http_request_duration_seconds_bucket', le, job: 'api' },
    values: Array.from({ length: 40 }, (_, i) => {
      // A latency shift halfway through: mass moves into the slower buckets.
      const shifted = i > 20 ? 2.5 : 0;
      const centre = 4 + shifted;
      const weight = Math.exp(-((bucketIndex - centre) ** 2) / 4) * 120;
      // Cumulative, as Prometheus reports it.
      const cumulative = bounds
        .slice(0, bucketIndex + 1)
        .reduce((sum, _, j) => sum + Math.exp(-((j - centre) ** 2) / 4) * 120, 0);
      return [start + i * 90, String(Math.round(cumulative + weight * 0))] as [number, string];
    }),
  }));
  const payload = { data, exploreUrl: 'https://example.grafana.net/explore?panes=%7B%7D' };
  return {
    content: [{ type: 'text' as const, text: JSON.stringify(payload) }],
    structuredContent: payload,
    isError: false,
    _meta: { ui: { resourceUri: 'ui://grafana/metrics.html' } },
  };
}

function toolResult(generation = 0) {
  const start = Math.floor(Date.now() / 1000) - 3600;
  const values: Array<[number, string]> = Array.from({ length: 60 }, (_, i) => [
    start + i * 60,
    String((2 + Math.sin(i / 4) * 1.5 + (i > 20 && i < 30 ? 30 : 0)).toFixed(3)),
  ]);
  const payload = {
    // The generation offset makes a refresh visibly different from the first render.
    data: [
      {
        metric: { __name__: 'node_load1', instance: 'MacBookPro.mynet' },
        values: values.map(([at, v]) => [at, String((Number(v) + generation).toFixed(3))] as [number, string]),
      },
    ],
    exploreUrl: 'https://example.grafana.net/explore?panes=%7B%7D',
  };
  return {
    content: [{ type: 'text' as const, text: JSON.stringify(payload) }],
    structuredContent: payload,
    isError: false,
    _meta: { ui: { resourceUri: 'ui://grafana/metrics.html' } },
  };
}

async function main() {
  const params = new URLSearchParams(location.search);
  const appFile = params.get('app') ?? 'metrics.html';
  const theme = params.get('mode') === 'dark' ? 'dark' : 'light';

  const iframe = document.getElementById('app') as HTMLIFrameElement;
  // dist/ is the dev server's publicDir, so bundles are same-origin here and
  // the iframe's console is reachable.
  iframe.src = `/${appFile}`;
  record(`loading /${appFile}`);

  await new Promise<void>((resolve) => {
    iframe.addEventListener('load', () => resolve(), { once: true });
  });
  record('iframe loaded');

  const bridge = new AppBridge(
    null,
    { name: 'local-debug-host', version: '0.1.0' },
    { /* host capabilities: none needed to deliver a tool result */ }
  );

  bridge.onsizechange = (params) => record(`size-changed: ${JSON.stringify(params)}`);

  // The app calls back for Refresh; answer it the way the MCP server would so
  // the round trip is exercised, not stubbed out.
  bridge.oncalltool = async (params) => {
    record(`app → callServerTool: ${params.name} ${JSON.stringify(params.arguments)}`);
    if (params.name !== 'query_prometheus') {
      return { content: [{ type: 'text', text: `unknown tool ${params.name}` }], isError: true };
    }
    refreshes += 1;
    return toolResult(refreshes);
  };

  // "Ask about a window" sends a user message to the agent.
  bridge.onmessage = async (params) => {
    const text = params.content.map((block) => ('text' in block ? block.text : block.type)).join(' ');
    record(`app → sendMessage: ${text.slice(0, 160)}`);
    return {};
  };

  const transport = new PostMessageTransport(iframe.contentWindow!, iframe.contentWindow!);
  await bridge.connect(transport);
  record('bridge connected');

  bridge.setHostContext({ theme });
  record(`host context set (theme=${theme})`);

  const fixtureName = params.get('fixture');
  const fixtureExpr: Record<string, string> = {
    bounded1: 'node_memory_utilisation_ratio',
    boundedN: 'node_filesystem_used_ratio',
    instantN: 'topk(3, http_requests_total)',
    single: 'go_goroutines',
    buckets: 'rate(http_request_duration_seconds_bucket[5m])',
  };
  await bridge.sendToolInput({
    toolName: 'query_prometheus',
    arguments: {
      expr: fixtureName ? (fixtureExpr[fixtureName] ?? fixtureName) : 'node_load1',
      datasourceUid: 'grafanacloud-prom',
      queryType: fixtureName && fixtureName !== 'buckets' ? 'instant' : 'range',
    },
  } as never);
  record('tool-input sent');

  const payload =
    fixtureName === 'buckets' ? bucketResult() : fixtureName ? instantResult(fixtureName) : toolResult();
  await bridge.sendToolResult(payload as never);
  record(`tool-result sent${fixtureName ? ` (fixture=${fixtureName})` : ''}`);

  // `?drive=refresh|brush` exercises an interaction without a human, so both
  // round trips can be verified headlessly.
  const drive = params.get('drive');
  if (drive) {
    const doc = iframe.contentDocument;
    if (!doc) {
      record('drive: iframe document unavailable');
      return;
    }

    // Poll rather than sleep: under Chrome's virtual time budget a fixed
    // setTimeout can fast-forward past the app's first paint.
    const waitFor = async <T>(what: string, probe: () => T | undefined | null): Promise<T | undefined> => {
      for (let attempt = 0; attempt < 60; attempt += 1) {
        const found = probe();
        if (found) return found;
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
      record(`drive: timed out waiting for ${what}`);
      return undefined;
    };

    if (drive === 'refresh') {
      const button = await waitFor('the Refresh button', () =>
        [...doc.querySelectorAll('button')].find((b) => /refresh/i.test(b.textContent ?? ''))
      );
      if (!button) {
        record(`drive: buttons present = [${[...doc.querySelectorAll('button')].map((b) => b.textContent).join(' | ')}]`);
        return;
      }
      record('drive: clicking Refresh');
      button.click();
      await waitFor('the refresh round trip', () => (refreshes > 0 ? true : undefined));
      record('drive: refresh settled');
    }

    // `?drive=viz:<kind>` opens the picker and selects a visualization.
    if (drive.startsWith('viz')) {
      const trigger = await waitFor('the viz picker', () =>
        [...doc.querySelectorAll('button[aria-haspopup="listbox"]')][0] as HTMLButtonElement | undefined
      );
      if (!trigger) return;
      trigger.click();
      const wanted = drive.split(':')[1];
      const options = await waitFor('the picker menu', () => {
        const found = [...doc.querySelectorAll('[role="option"]')];
        return found.length ? found : undefined;
      });
      record(`drive: menu = [${(options ?? []).map((o) => o.textContent).join(' | ')}]`);
      if (wanted) {
        const option = (options ?? []).find((o) => new RegExp(wanted, 'i').test(o.textContent ?? ''));
        if (option) {
          (option as HTMLButtonElement).click();
          record(`drive: selected ${wanted}`);
        }
      }
      await new Promise((resolve) => setTimeout(resolve, 300));
    }

    if (drive === 'brush') {
      const toggle = await waitFor('the window-select toggle', () =>
        [...doc.querySelectorAll('button')].find((b) => /ask about a window/i.test(b.textContent ?? ''))
      );
      if (!toggle) return;
      record('drive: arming brush');
      toggle.click();

      const canvas = await waitFor('the chart canvas', () => doc.querySelector('canvas'));
      if (!canvas) return;
      const box = canvas.getBoundingClientRect();
      const y = box.top + box.height / 2;
      const drag = (type: string, x: number) =>
        canvas.dispatchEvent(
          new MouseEvent(type, { bubbles: true, clientX: x, clientY: y, buttons: type === 'mouseup' ? 0 : 1 })
        );
      drag('mousedown', box.left + box.width * 0.3);
      drag('mousemove', box.left + box.width * 0.45);
      drag('mousemove', box.left + box.width * 0.6);
      drag('mouseup', box.left + box.width * 0.6);
      await waitFor('the selection to reach the agent', () =>
        log.some((line) => line.includes('sendMessage')) ? true : undefined
      );
      record('drive: brush settled');
    }
  }
}

main().catch((cause) => record(`HOST FAILED: ${cause instanceof Error ? cause.stack : String(cause)}`));
