import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig, type Plugin } from 'vite';
import { viteSingleFile } from 'vite-plugin-singlefile';

const packageRoot = fileURLToPath(new URL('.', import.meta.url));

/**
 * Embeds the vendored Grafana ECharts renderer as gzip + base64, after checking
 * it against vendor/renderer.json. The App decodes it on first use
 * (app/datasourceRenderer.ts).
 */
const echartsRenderer = (): Plugin => ({
  name: 'grafana-echarts-renderer',
  transformIndexHtml() {
    const bundle = readFileSync(resolve(packageRoot, 'vendor', 'grafana-echarts-viz.js'));
    const pin = JSON.parse(readFileSync(resolve(packageRoot, 'vendor', 'renderer.json'), 'utf8'));
    const sha256 = createHash('sha256').update(bundle).digest('hex');
    if (sha256 !== pin.sha256) {
      throw new Error(`vendor/grafana-echarts-viz.js has SHA-256 ${sha256}, not the ${pin.sha256} of vendor/renderer.json.`);
    }
    return [{
      tag: 'script',
      attrs: { id: 'grafana-echarts-renderer', type: 'application/gzip;base64', 'data-version': pin.version },
      children: gzipSync(bundle, { level: 9 }).toString('base64'),
      injectTo: 'body-prepend',
    }];
  },
});

/** Build config for one app bundle, `app/<entry>` → `dist/<entry>`. */
export function appBuildConfig(entry: string, { renderer = false }: { renderer?: boolean } = {}) {
  return defineConfig({
    base: './',
    plugins: [...(renderer ? [echartsRenderer()] : []), viteSingleFile()],
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
