import { defineConfig } from 'vitest/config';
import { sharedModulesPlugin } from './sharedModules.config';

export default defineConfig({
  plugins: [sharedModulesPlugin()],
  test: {
    environment: 'jsdom',
    include: ['test/**/*.test.{ts,tsx}'],
    restoreMocks: true,
    // Design packages import their token CSS; let Vite transform those imports in jsdom.
    server: { deps: { inline: ['@grafana/ui', '@grafana/data'] } },
  },
});
