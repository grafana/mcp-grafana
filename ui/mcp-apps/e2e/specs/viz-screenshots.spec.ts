import { test, expect } from '@playwright/test';
import { existsSync } from 'node:fs';
import charts from '@grafana/viz-contract/registry/chart-types.json' with { type: 'json' };

// Iterate the pinned registry, so newly supported chart types cannot silently miss coverage.
for (const { name } of charts.charts) {
  test(`visual smoke: ${name}`, async ({ page }, info) => {
    expect(existsSync(new URL(`../../node_modules/@grafana/viz-contract/fixtures/smoke-${name}/spec.json`, import.meta.url)),
      `Contract needs a smoke fixture for ${name}`).toBe(true);
    await page.clock.setSystemTime(new Date('2026-09-25T16:24:38Z'));
    await page.addInitScript(() => {
      (window as unknown as { cspViolations: string[] }).cspViolations = [];
      document.addEventListener('securitypolicyviolation', event => {
        (window as unknown as { cspViolations: string[] }).cspViolations.push(`${event.effectiveDirective}: ${event.blockedURI}`);
      });
      let seed = 1643;
      Math.random = () => { seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0; return seed / 4294967296; };
    });
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(`/?chart=${encodeURIComponent(name)}`);
    const app = page.frameLocator('#app').frameLocator('iframe');
    const chart = app.getByTestId('viz-renderer');
    try {
      await expect(chart).toHaveAttribute('data-viz-state', 'ready');
      await chart.evaluate(async () => { await document.fonts.ready; });
      await expect(chart).toHaveScreenshot(`${name}.png`, {
        animations: 'disabled', caret: 'hide', scale: 'css',
        maxDiffPixelRatio: 0.002,
      });
      expect(errors).toEqual([]);
      expect(await chart.evaluate(() => (window as unknown as { cspViolations: string[] }).cspViolations)).toEqual([]);
    } finally {
      await info.attach('browser-errors', { body: errors.join('\n'), contentType: 'text/plain' });
      await info.attach('host-log', { body: await page.locator('#log').innerText(), contentType: 'text/plain' });
      await info.attach('rendered-page', { body: await page.screenshot(), contentType: 'image/png' });
    }
  });
}
