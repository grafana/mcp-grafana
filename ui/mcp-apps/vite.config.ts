import { defineConfig } from 'vite';
import packageJson from './package.json';

const externalPackages = [...Object.keys(packageJson.dependencies), 'react', 'react-dom'];

export default defineConfig({
  build: {
    lib: { entry: 'src/index.ts', formats: ['es'], fileName: 'index' },
    rollupOptions: {
      external: (id) => externalPackages.some((name) => id === name || id.startsWith(`${name}/`)),
    },
  },
});
