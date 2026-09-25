import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'jsdom',
    include: ['test/**/*.test.tsx'],
    restoreMocks: true,
    // Design packages import their token CSS; let Vite transform those imports in jsdom.

  },
});
