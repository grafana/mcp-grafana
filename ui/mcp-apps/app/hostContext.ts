import type { App } from '@modelcontextprotocol/ext-apps';

import type { McpAppColorMode } from '../src/McpAppShell';

type HostContext = Parameters<NonNullable<App['onhostcontextchanged']>>[0];

/**
 * Applies the host context to the page and returns the color mode it sets.
 *
 * The theme also goes on the document root, which sets its `color-scheme`. A
 * browser paints an iframe's canvas opaque when its color scheme differs from
 * the embedding page's, so a light root in a dark host shows a white box
 * around the card; matching the host keeps the canvas transparent.
 */
export function applyHostContext(context: HostContext): McpAppColorMode | undefined {
  if (context.safeAreaInsets) {
    const { top, right, bottom, left } = context.safeAreaInsets;
    document.body.style.padding = `${top}px ${right}px ${bottom}px ${left}px`;
    document.body.style.boxSizing = 'border-box';
  }
  if (context.theme === 'light' || context.theme === 'dark') {
    document.documentElement.dataset.colorMode = context.theme;
    return context.theme;
  }
  return undefined;
}
