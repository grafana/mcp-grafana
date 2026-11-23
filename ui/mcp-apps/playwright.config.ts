import { appPort, appUrl, sandboxUrl } from './harness/ports.mjs';
import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: './e2e/specs', timeout: 30_000, fullyParallel: true,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: { browserName: 'chromium', baseURL: appUrl,
    viewport: { width: 1200, height: 1000 }, locale: 'en-US', timezoneId: 'UTC',
    reducedMotion: 'reduce', trace: 'retain-on-failure', screenshot: 'only-on-failure' },
  webServer: [
    { command: 'node harness/basic-host/start.mjs', url: sandboxUrl, reuseExistingServer: !process.env.CI },
    // Local startup may download modules and compile Go from a cold cache; CI supplies a binary.
    { command: 'npx vite --config harness/vite.config.ts', port: appPort, timeout: process.env.MCP_GRAFANA_BINARY ? 180_000 : 600_000, reuseExistingServer: !process.env.CI },
  ],
});
