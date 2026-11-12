import type { MouseEvent } from 'react';

import type { McpAppOpenInGrafana } from './McpAppShell';

/** Only plain http(s) links without credentials reach the shell's header action. */
export function isSafeGrafanaUrl(value: string | undefined): value is string {
  if (!value) return false;
  try {
    const url = new URL(value);
    return (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password;
  } catch {
    return false;
  }
}

/**
 * Builds the shell's "Open in Grafana" action. A sandboxed iframe cannot
 * navigate on its own, so when `onOpen` is given the click is handed to the
 * host instead of the anchor.
 */
export function openInGrafanaAction(
  url: string | undefined,
  onOpen?: (target: { url: string }) => void
): McpAppOpenInGrafana | undefined {
  if (!isSafeGrafanaUrl(url)) return undefined;
  return {
    href: url,
    onClick: onOpen
      ? (event: MouseEvent<HTMLAnchorElement>) => {
          event.preventDefault();
          onOpen({ url });
        }
      : undefined,
  };
}
