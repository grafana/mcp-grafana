import { defineConfig } from 'vitest/config';
import { sharedModulesPlugin } from './sharedModules.config';

export default defineConfig({
  plugins: [sharedModulesPlugin()],
  // Tests read no built Renderer; the App build sets the real URI (app.vite.config.ts).
  define: { __VIZ_RENDERER_URI__: JSON.stringify('ui://grafana/viz-renderer-test.js') },
  test: {
    environment: 'jsdom',
    include: ['test/**/*.test.{ts,tsx}'],
    // Downloads the pinned viz Renderer into renderer/ before any test file imports it.
    globalSetup: ['./test/renderer.global-setup.ts'],
    restoreMocks: true,
    coverage: {
      provider: 'v8',
      reporter: ['text-summary', 'html', 'json-summary'],
      // Include unimported production files in the denominator, not just exercised modules.
      include: ['src/**/*.{ts,tsx}', 'app/**/*.{ts,tsx}'],
      exclude: ['**/*.d.ts'],
      reportsDirectory: './coverage',
    },
    // Design packages import their token CSS; let Vite transform those imports in jsdom.
    server: { deps: { inline: ['@grafana/ui', '@grafana/data'] } },
  },
});
