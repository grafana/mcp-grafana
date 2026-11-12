import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  server: { host: '127.0.0.1', port: 5186, strictPort: true },
  build: { outDir: '../dist-preview', emptyOutDir: true },
});
