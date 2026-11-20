import { defineConfig } from 'vite';
import { LIBRARY_ENTRIES, shellEntryGuard } from './entryPoints.config';
import packageJson from './package.json';

// The library bundles no package that a consumer installs: its dependencies and its peer dependencies stay imports.
const externalPackages = [...Object.keys(packageJson.dependencies), ...Object.keys(packageJson.peerDependencies)];

export default defineConfig({
  plugins: [shellEntryGuard()],
  build: {
    lib: { entry: { ...LIBRARY_ENTRIES }, formats: ['es'] },
    rollupOptions: {
      external: (id) => externalPackages.some((name) => id === name || id.startsWith(`${name}/`)),
    },
  },
});
