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

/** The id of the style element of the bundle CSS, so a second run of the script does not add it again. */
const STYLE_ID = 'grafana-viz-renderer-css';

/**
 * Keeps the resource one file. Library mode writes the CSS of the bundle, for example the rc-slider styles that
 * @grafana/ui imports, to a file of its own, which Go does not embed. This moves it into a style element that the
 * script adds once, first in head, and fails the build on any other file. Then it writes HASH_FILE_NAME.
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
        const source = typeof file.source === 'string' ? file.source : new TextDecoder().decode(file.source);
        css += source.replace(/\/\*\$vite\$:\d+\*\/\n?/g, '');
        delete bundle[file.fileName];
      }
      if (css) {
        // First in head, as the inline path has it, so @grafana/ui Emotion rules win ties with the defaults.
        chunk.code += `;(()=>{if(document.getElementById(${JSON.stringify(STYLE_ID)}))return;const s=document.createElement("style");s.id=${JSON.stringify(STYLE_ID)};s.textContent=${JSON.stringify(css)};document.head.prepend(s)})();\n`;
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
