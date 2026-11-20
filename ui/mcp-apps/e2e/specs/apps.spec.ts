import { test, expect } from '@playwright/test';
import args from '@grafana/viz-contract/fixtures/prometheus-line/args.json' with { type: 'json' };

const browserErrors = new WeakMap<object, string[]>();

test.beforeEach(async ({ page }) => {
  const diagnostics: string[] = [];
  page.on('pageerror', error => diagnostics.push(`pageerror: ${error.message}`));
  page.on('console', message => { if (message.type() === 'error') diagnostics.push(`console: ${message.text()}`); });
  browserErrors.set(page, diagnostics);
  await page.clock.setFixedTime(new Date('2026-01-01T12:00:00Z'));
});
test.afterEach(async ({ page }, info) => {
  await info.attach('browser-errors', { body: (browserErrors.get(page) ?? []).join('\n'), contentType: 'text/plain' });
  await info.attach('host-log', { body: await page.locator('#log').textContent() ?? '', contentType: 'text/plain' });
});

for (const policy of ['forward', 'tool-declared']) {
  test(`renders chart with ${policy} resource policy`, async ({ page }) => {
    await page.goto(`/?policy=${policy}`);
    await expect(page.locator('#log')).toContainText('initialized');
    await expect(page.locator('#log')).toContainText('result-delivered');
    const app = page.frameLocator('#app').frameLocator('iframe');
    await expect(app.locator('canvas').first()).toBeVisible();
    await expect.poll(() => app.locator('canvas').first().evaluate((canvas: HTMLCanvasElement) => {
      const pixels = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data;
      const colors = new Set<number>();
      for (let i = 0; i < pixels.length; i += 4) {
        if (pixels[i + 3]) colors.add((pixels[i] << 16) | (pixels[i + 1] << 8) | pixels[i + 2]);
      }
      return colors.size;
    })).toBeGreaterThan(10);
    await expect(app.getByText(/Could not read the chart script/)).toHaveCount(0);
  });
}

test('refresh calls query tool and displays replacement title', async ({ page }) => {
  await page.goto('/');
  const app = page.frameLocator('#app').frameLocator('iframe');
  await expect(app.locator('canvas').first()).toBeVisible();
  await app.getByRole('button', { name: /Refresh/ }).click();
  await expect(page.locator('#log')).toContainText('tool-call');
  const calls = await page.locator('#log').textContent();
  const { name, arguments: actualArguments } = JSON.parse(calls!.split('\n').find(line => line.startsWith('tool-call '))!.slice(10));
  expect({ name, arguments: actualArguments }).toEqual({ name: 'render_grafana_app', arguments: { toolName: 'query_datasource', arguments: args } });
  await expect(page.locator('#log')).toContainText('"name":"render_grafana_app"');
  await expect(page.locator('#log')).toContainText('"toolName":"query_datasource"');
  await expect(app.getByText('Refreshed request rate')).toBeVisible();
});

test('trace selection updates span details', async ({ page }) => {
  await page.goto('/?scenario=trace');
  const app = page.frameLocator('#app').frameLocator('iframe');
  await expect(app.getByRole('region', { name: 'Selected span' })).toContainText('authorize payment');
  await app.getByRole('option', { name: 'frontend, POST /checkout/batch', exact: true }).click();
  await expect(app.getByRole('region', { name: 'Selected span' })).toContainText('POST /checkout/batch');
});

test('theme and width updates keep chart usable', async ({ page }) => {
  await page.goto('/');
  const app = page.frameLocator('#app').frameLocator('iframe');
  await expect(app.locator('canvas').first()).toBeVisible();
  await expect(app.locator('[data-color-mode]').first()).toHaveAttribute('data-color-mode', 'light');
  await page.getByRole('button', { name: 'Toggle theme' }).click();
  await expect(app.locator('[data-color-mode]').first()).toHaveAttribute('data-color-mode', 'dark');
  await page.getByRole('button', { name: 'Resize', exact: true }).click();
  await expect(page.locator('#app')).toHaveCSS('width', '520px');
  await expect(app.locator('canvas').first()).toBeVisible();
});

test('panel viewer displays decoded image and opens Grafana through host', async ({ page }) => {
  await page.goto('/?scenario=panel');
  const app = page.frameLocator('#app').frameLocator('iframe');
  const image = app.getByRole('img', { name: 'Grafana panel' });
  await expect(image).toBeVisible();
  await expect.poll(() => image.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(1);
  await app.getByRole('link', { name: 'Open in Grafana' }).click();
  await expect(page.locator('#log')).toContainText('open-link {"url":"https://example.grafana.net/d/example?viewPanel=1"}');
});

test('panel viewer reports tool errors', async ({ page }) => {
  await page.goto('/?scenario=panel-error');
  await expect(page.frameLocator('#app').frameLocator('iframe').getByText('Panel rendering failed: fixture error')).toBeVisible();
});

for (const scenario of ['empty', 'error']) {
  test(`query ${scenario} has usable feedback`, async ({ page }) => {
    await page.goto(`/?scenario=${scenario}`);
    const app = page.frameLocator('#app').frameLocator('iframe');
    await expect(app.getByText(scenario === 'empty' ? /No data/i : /The query failed. Check the query inputs/).first()).toBeVisible();
  });
}

test('interactive preview switches between metrics and trace using shared host', async ({ page }) => {
  await page.goto('/?scenario=metrics');
  const app = page.frameLocator('#app').frameLocator('iframe');
  await expect(app.locator('canvas').first()).toBeVisible();
  await page.getByLabel('Scenario').selectOption('trace');
  await expect(app.getByRole('region', { name: 'Selected span' })).toContainText('authorize payment');
  await page.getByLabel('Scenario').selectOption('chart:barchart');
  await expect(app.getByTestId('viz-renderer')).toHaveAttribute('data-viz-state', 'ready');
  await expect(app.locator('canvas').first()).toBeVisible();
});
