import type { Decorator, Preview } from '@storybook/react-vite';

import { exposeSharedModules } from '../app/sharedModules';
import '../styles.css';

// The viz Renderer takes React and Emotion from this global instead of bundling a copy (#1589).
exposeSharedModules();

/** Gives each story a page background that follows the toolbar's color mode. The shell sets its own data-color-mode. */
const withPage: Decorator = (Story, context) => {
  const dark = context.globals.colorMode === 'dark';
  return (
    <div
      data-color-mode={context.globals.colorMode}
      style={{
        minHeight: '100vh',
        padding: 16,
        boxSizing: 'border-box',
        background: dark ? '#111217' : '#f4f5f5',
      }}
    >
      <Story />
    </div>
  );
};

const preview: Preview = {
  decorators: [withPage],
  globalTypes: {
    colorMode: {
      description: 'Color mode passed to the App',
      toolbar: {
        title: 'Color mode',
        icon: 'mirror',
        dynamicTitle: true,
        items: [
          { value: 'light', title: 'Light' },
          { value: 'dark', title: 'Dark' },
        ],
      },
    },
  },
  initialGlobals: { colorMode: 'light' },
  parameters: { layout: 'fullscreen', controls: { disable: true } },
};

export default preview;
