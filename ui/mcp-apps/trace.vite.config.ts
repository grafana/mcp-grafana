import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import { viteSingleFile } from 'vite-plugin-singlefile';

const packageRoot = fileURLToPath(new URL('.', import.meta.url));

export default defineConfig({
  base: './',
  plugins: [viteSingleFile()],
  root: resolve(packageRoot, 'app'),
  build: {
    // MCP hosts read this app as one text resource. Force every font, image,
    // style, and script into the HTML so the resource needs no CSP exceptions.
    assetsInlineLimit: Number.MAX_SAFE_INTEGER,
    cssMinify: true,
    emptyOutDir: false,
    minify: true,
    outDir: resolve(packageRoot, 'dist'),
    rollupOptions: {
      input: resolve(packageRoot, 'app/trace.html'),
    },
  },
});
