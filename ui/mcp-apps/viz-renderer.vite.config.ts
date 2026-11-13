import { createHash } from 'node:crypto';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig, type Plugin } from 'vite';
import { sharedModulesExternal, sharedModulesGuard } from './sharedModules.config';

const packageRoot = fileURLToPath(new URL('.', import.meta.url));

/** The global that the Renderer resource sets to the Renderer module when it runs as an inline script. */
export const VIZ_RENDERER_GLOBAL = '__grafanaVizRenderer';

const FILE_NAME = 'viz-renderer.js';

/** Holds the first 16 hex digits of the sha256 of FILE_NAME, for its URI, so Go does not hash 3.7 MB at start. */
const HASH_FILE_NAME = 'viz-renderer.hash';

/**
 * Keeps the resource one file. Library mode writes the CSS of the bundle, for example the rc-slider styles that
 * @grafana/ui imports, to a file of its own, which Go does not embed. This moves it into a style element that the
 * script adds when it runs, and fails the build on any other file. Then it writes HASH_FILE_NAME.
 */
function singleScript(): Plugin {
  return {
    name: 'grafana-viz-renderer-single-script',
    enforce: 'post',
    generateBundle(_options, bundle) {
      const chunk = bundle[FILE_NAME];
      if (chunk?.type !== 'chunk') return this.error(`no ${FILE_NAME} in the bundle`);
      let css = '';
      for (const file of Object.values(bundle)) {
        if (file === chunk) continue;
        if (file.type !== 'asset' || !file.fileName.endsWith('.css')) {
          return this.error(`${file.fileName} is not in ${FILE_NAME}; the resource must be one file`);
        }
        css += String(file.source).replace(/\/\*\$vite\$:\d+\*\/\n?/g, '');
        delete bundle[file.fileName];
      }
      if (css) {
        chunk.code += `;(()=>{const s=document.createElement("style");s.textContent=${JSON.stringify(css)};document.head.appendChild(s)})();\n`;
      }
      const hash = createHash('sha256').update(chunk.code).digest('hex').slice(0, 16);
      this.emitFile({ type: 'asset', fileName: HASH_FILE_NAME, source: `${hash}\n` });
    },
  };
}

// The Renderer resource, dist/viz-renderer.js: one classic script, so the App can run it as an inline script under
// the default CSP of MCP Apps (#1589). The shared packages come from the App's global, not from this bundle.
export default defineConfig({
  plugins: [sharedModulesGuard(), singleScript()],
  build: {
    emptyOutDir: false,
    minify: true,
    outDir: resolve(packageRoot, 'dist'),
    lib: {
      entry: resolve(packageRoot, 'app', 'vizRenderer.entry.ts'),
      formats: ['iife'],
      name: VIZ_RENDERER_GLOBAL,
      fileName: () => FILE_NAME,
    },
    rollupOptions: {
      external: sharedModulesExternal.external,
      output: {
        globals: sharedModulesExternal.globals,
        inlineDynamicImports: true,
      },
    },
  },
  define: { 'process.env.NODE_ENV': JSON.stringify('production') },
});
