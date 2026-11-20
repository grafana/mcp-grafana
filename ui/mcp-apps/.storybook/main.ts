import type { StorybookConfig } from '@storybook/react-vite';
import { mergeConfig } from 'vite';

import { sharedModulesPlugin } from '../sharedModules.config.ts';

// Storybook has its own Vite config on purpose: the App and Renderer bundles under dist/ are committed, so nothing here
// may touch vite.config.ts, app.vite.config.ts or their inputs.
const config: StorybookConfig = {
  stories: ['../stories/**/*.stories.tsx'],
  framework: '@storybook/react-vite',
  core: { disableTelemetry: true },
  viteFinal: (base) =>
    mergeConfig(base, {
      // Same as the App build: app/sharedModules.ts imports this virtual module.
      plugins: [sharedModulesPlugin()],
      // loadRenderer.ts reads the Renderer URI from the build. Stories serve the committed Renderer under this URI.
      define: { __VIZ_RENDERER_URI__: JSON.stringify('ui://grafana/viz-renderer-storybook.js') },
    }),
};

export default config;
