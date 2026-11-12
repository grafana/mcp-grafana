import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

const packageRoot = fileURLToPath(new URL('.', import.meta.url));

// Dev-only host harness. Serves `preview/localhost.html` and exposes the built
// app bundles under /dist so the iframe is same-origin and its console is
// reachable — which a real host's sandbox does not allow.
export default defineConfig({
  root: resolve(packageRoot, 'preview'),
  publicDir: resolve(packageRoot, 'dist'),
  server: { fs: { allow: [packageRoot] } },
});
