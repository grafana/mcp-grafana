import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import { viteSingleFile } from 'vite-plugin-singlefile';
import { sharedModulesPlugin } from './sharedModules.config';
import { VIZ_RENDERER_HASH_FILE, vizRendererUri } from './vizRenderer.config';

const packageRoot = fileURLToPath(new URL('.', import.meta.url));

/**
 * The URI of the Renderer resource that the App reads at run time. The Renderer is built first (build:apps), so its
 * hash is in dist/ by now.
 */
function vizRendererUriFromBuild(): string {
  const file = resolve(packageRoot, 'dist', VIZ_RENDERER_HASH_FILE);
  if (!existsSync(file)) throw new Error(`${file} is missing: build the Renderer resource before the App`);
  return vizRendererUri(readFileSync(file, 'utf8'));
}

/** Build config for one app bundle, `app/<entry>` → `dist/<entry>`. */
export function appBuildConfig(entry: string) {
  return defineConfig({
    base: './',
    define: { __VIZ_RENDERER_URI__: JSON.stringify(vizRendererUriFromBuild()) },
    plugins: [sharedModulesPlugin(), viteSingleFile()],
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
