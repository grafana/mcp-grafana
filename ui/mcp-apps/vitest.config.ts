import { defineConfig } from 'vitest/config';
import { sharedModulesPlugin } from './sharedModules.config';

export default defineConfig({
  plugins: [sharedModulesPlugin()],
  // Tests read no built Renderer; the App build sets the real URI (app.vite.config.ts).
  define: { __VIZ_RENDERER_URI__: JSON.stringify('ui://grafana/viz-renderer-test.js') },
  test: {
    environment: 'jsdom',
    include: ['test/**/*.test.{ts,tsx}'],
    restoreMocks: true,
    // Design packages import their token CSS; let Vite transform those imports in jsdom.
    server: { deps: { inline: ['@grafana/ui', '@grafana/data'] } },
  },
});
