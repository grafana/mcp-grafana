import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'jsdom',
    include: ['test/**/*.test.{ts,tsx}'],
    // Downloads the pinned viz Renderer into renderer/ before any test file imports it.
    globalSetup: ['./test/renderer.global-setup.ts'],
    restoreMocks: true,
    // Design packages import their token CSS; let Vite transform those imports in jsdom.
    server: { deps: { inline: ['@grafana/ui', '@grafana/data'] } },
  },
});
