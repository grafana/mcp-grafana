import { readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig, type Plugin } from 'vite';
import { viteSingleFile } from 'vite-plugin-singlefile';

const packageRoot = fileURLToPath(new URL('.', import.meta.url));

// SPIKE option C′: BEYOND_RENDERER=<path to grafana-echarts-viz.js> embeds the
// grafana-beyond renderer as gzip + base64, the fork's ui/grafana-viz scheme.
const beyondRenderer = (): Plugin => ({
  name: 'beyond-renderer',
  transformIndexHtml() {
    const path = process.env.BEYOND_RENDERER;
    if (!path) return [];
    return [{
      tag: 'script',
      attrs: { id: 'grafana-viz-renderer', type: 'application/gzip;base64' },
      children: gzipSync(readFileSync(path), { level: 9 }).toString('base64'),
      injectTo: 'body-prepend',
    }];
  },
});

/** Build config for one app bundle, `app/<entry>` → `dist/<entry>`. */
export function appBuildConfig(entry: string) {
  return defineConfig({
    base: './',
    plugins: [beyondRenderer(), viteSingleFile()],
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
        input: resolve(packageRoot, 'app', entry),
      },
    },
  });
}
