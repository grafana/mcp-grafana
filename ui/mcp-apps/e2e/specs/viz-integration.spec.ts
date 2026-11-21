import { test, expect, type Page, type FrameLocator } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import args from '@grafana/viz-contract/fixtures/prometheus-line/args.json' with { type: 'json' };
import result from '@grafana/viz-contract/fixtures/prometheus-line/result.json' with { type: 'json' };

const browserErrors = new WeakMap<Page, string[]>();

const appFrame = (page: Page) => page.frameLocator('#app').frameLocator('iframe');
const events = async (page: Page, name: string) => (await page.locator('#log').innerText())
  .split('\n').filter(line => line.startsWith(`${name} `)).map(line => JSON.parse(line.slice(name.length + 1)));

async function rendered(app: FrameLocator) {
  await expect(app.getByTestId('viz-renderer')).toHaveAttribute('data-viz-state', 'ready');
  await expect.poll(() => app.locator('canvas').first().evaluate((canvas: HTMLCanvasElement) => {
    const pixels = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data;
    const colors = new Set<number>();
    for (let i = 0; i < pixels.length; i += 4) {
      if (pixels[i + 3]) colors.add((pixels[i] << 16) | (pixels[i + 1] << 8) | pixels[i + 2]);
    }
    return colors.size;
  })).toBeGreaterThan(10);
}

test.beforeEach(async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  browserErrors.set(page, errors);
  await page.clock.setSystemTime(new Date('2026-09-25T16:24:38Z'));
});

test.afterEach(async ({ page }, info) => {
  await info.attach('browser-errors', { body: (browserErrors.get(page) ?? []).join('\n'), contentType: 'text/plain' });
  await info.attach('host-log', { body: await page.locator('#log').innerText(), contentType: 'text/plain' });
  expect(browserErrors.get(page)).toEqual([]);
});

test('strict opaque-origin sandbox renders with localStorage denied (renderer.6, renderer.8)', async ({ page }) => {
  await page.goto('/?sandbox=strict');
  const app = page.frameLocator('#app');
  await expect(page.locator('#app')).toHaveAttribute('sandbox', 'allow-scripts');
  await rendered(app);
  expect(await app.locator('body').evaluate(() => {
    try { localStorage.getItem('probe'); return 'allowed'; }
    catch (error) { return (error as DOMException).name; }
  })).toBe('SecurityError');
  await app.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(app.getByText('Refreshed request rate')).toBeVisible();
  await rendered(app);
});

test('time picker sends range through bridge and updates model context (refresh.3, state.14)', async ({ page }) => {
  await page.goto('/');
  const app = appFrame(page);
  await rendered(app);
  await app.getByTestId('data-testid TimePicker Open Button').click();
  await app.getByText('Last 6 hours', { exact: true }).click();
  await expect.poll(() => events(page, 'tool-call')).toEqual([
    expect.objectContaining({ name: 'render_grafana_app', arguments: {
      toolName: 'query_datasource', arguments: { ...args, from: 'now-6h', to: 'now' },
    } }),
  ]);
  await expect(app.getByText('Refreshed request rate')).toBeVisible();
  await rendered(app);
  await expect(app.getByRole('button', { name: 'Refresh', exact: true })).toBeEnabled();
  await expect.poll(() => events(page, 'model-context')).toContainEqual({ content: [{ type: 'text', text:
    `The Grafana App shows a timeseries chart of data source ${args.datasourceUid}.\n` +
    'Time range of the arguments: now-6h to now.\n' +
    `Time range of the data: ${new Date(result.data.range.from).toISOString()} to ${new Date(result.data.range.to).toISOString()}.\n` +
    'State: ready.',
  }] });
});

test('failed refresh preserves chart and successful retry clears error (refresh.5, refresh.11)', async ({ page }) => {
  await page.goto('/?refresh=fail-once');
  const app = appFrame(page);
  await rendered(app);
  const refresh = app.getByRole('button', { name: 'Refresh', exact: true });
  await refresh.click();
  await expect(app.getByText(/The data source did not answer/)).toBeVisible();
  await rendered(app);
  await expect(app.getByText('Refreshed request rate')).toHaveCount(0);
  await expect(refresh).toBeEnabled();
  await refresh.click();
  await expect(app.getByText('Refreshed request rate')).toBeVisible();
  await expect(app.getByText(/The data source did not answer/)).toHaveCount(0);
  await expect(refresh).toBeEnabled();
  await rendered(app);
  await expect.poll(() => events(page, 'tool-call')).toHaveLength(2);
});

test('real Go tool result renders and refreshes through MCP', async ({ page }) => {
  const datasourceUid = `e2e-${randomUUID()}`;
  await page.goto(`/?scenario=go&datasource=${datasourceUid}`);
  const app = appFrame(page);
  await rendered(app);
  await expect(app.getByText('Go integration chart')).toBeVisible();
  const expectedRequest = {
    from: String(result.data.range.from), to: String(result.data.range.to),
    queries: args.queries.map(query => ({ ...query, datasource: { uid: datasourceUid }, maxDataPoints: 1000, intervalMs: 1000 })),
  };
  await expect.poll(() => events(page, 'grafana-requests')).toEqual([[expectedRequest]]);
  const [initial] = await events(page, 'go-result');
  expect(initial.isError).not.toBe(true);
  expect(initial.structuredContent).toMatchObject({
    chart: 'timeseries', panel: { ...result.panel, title: 'Go integration chart' },
    data: { range: result.data.range, results: result.data.results },
  });
  await app.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect.poll(() => events(page, 'grafana-requests')).toEqual([[expectedRequest], [expectedRequest, expectedRequest]]);
  await expect(app.getByRole('button', { name: 'Refresh', exact: true })).toBeEnabled();
  await rendered(app);
});

// Exercise browser-only overlay states that the jsdom icon tests cannot render.
test('interactive chart controls render icons without network requests (delivery.7, delivery.8)', async ({ page }) => {
  await page.addInitScript(() => {
    const diagnostics = window as unknown as { cspViolations: string[] };
    diagnostics.cspViolations = [];
    document.addEventListener('securitypolicyviolation', event => {
      diagnostics.cspViolations.push(`${event.effectiveDirective}: ${event.blockedURI}`);
    });
  });
  const requests: string[] = [];
  page.on('request', request => {
    if (request.frame().parentFrame()?.parentFrame()) requests.push(request.url());
  });
  await page.goto('/');
  const app = appFrame(page);
  await rendered(app);
  await page.getByRole('button', { name: 'Resize', exact: true }).click();
  await expect(page.locator('#app')).toHaveCSS('width', '520px');
  await app.getByTestId('data-testid TimePicker Open Button').click();
  await expect(app.locator('#TimePickerContent')).toBeVisible();
  await expect(app.getByRole('button', { name: 'Open calendar' }).first().locator('svg path')).not.toHaveCount(0);
  await page.keyboard.press('Escape');
  const canvas = app.locator('canvas').first();
  const bounds = (await canvas.boundingBox())!;
  // Inside the plot of the fixed fixture; no ECharts internals or synthetic events.
  const point = { x: bounds.width * 0.4, y: bounds.height * 0.25 };
  await canvas.hover({ position: point });
  await expect(app.getByText('2026-09-25 16:14:00', { exact: true })).toBeVisible();
  await canvas.click({ position: point });
  await expect(app.getByTestId('icon-times')).toBeVisible();
  await expect(app.getByTestId('icon-times').locator('path')).not.toHaveCount(0);
  await app.getByRole('button', { name: 'Close', exact: true }).click();
  await app.getByRole('button', { name: /Edit color for/ }).first().click();
  await expect(app.getByRole('tab', { name: 'Colors', exact: true })).toBeVisible();
  await expect(app.getByRole('button', { name: 'red color', exact: true })).toBeVisible();
  await expect.poll(() => app.locator('[data-testid^="icon-"]').evaluateAll(icons =>
    icons.filter(icon => !icon.querySelector('path, circle, rect, line, polyline, polygon')).map(icon => icon.getAttribute('data-testid'))
  )).toEqual([]);
  expect(requests).toEqual([]);
  expect(await app.locator('body').evaluate(() => (window as unknown as { cspViolations: string[] }).cspViolations)).toEqual([]);
});
