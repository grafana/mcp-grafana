import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: './e2e/specs', timeout: 30_000, fullyParallel: true,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: { browserName: 'chromium', baseURL: 'http://127.0.0.1:5194',
    viewport: { width: 1200, height: 1000 }, locale: 'en-US', timezoneId: 'UTC',
    reducedMotion: 'reduce', trace: 'retain-on-failure', screenshot: 'only-on-failure' },
  webServer: [
    { command: 'node harness/basic-host/start.mjs', url: 'http://localhost:5197/sandbox.html', reuseExistingServer: !process.env.CI },
    // Local startup may download modules and compile Go from a cold cache; CI supplies a binary.
    { command: 'npx vite --config harness/vite.config.ts', port: 5194, timeout: process.env.MCP_GRAFANA_BINARY ? 180_000 : 600_000, reuseExistingServer: !process.env.CI },
  ],
});
