import type { StorageItem } from 'react-inlinesvg';

type Renderer = typeof import('../renderer/renderer.js');

let renderer: Promise<Renderer> | undefined;

/**
 * Loads the Renderer once. Before it resolves, the icon cache of the App's @grafana/ui holds each icon of icons.json,
 * so Icon makes no request in the sandbox (renderer.15). The other views do not load it.
 */
export function loadRenderer(): Promise<Renderer> {
  renderer ??= Promise.all([
    import('../renderer/renderer.js'),
    import('../renderer/icons.json'),
    import('react-inlinesvg'),
  ]).then(([module, { default: icons }, { cacheStore }]) => {
    fillIconCache(cacheStore, icons);
    return module;
  });
  return renderer;
}

/**
 * The URL that Icon of @grafana/ui requests for an icons.json key, with no window.__grafana_public_path__
 * (renderer.16). If the App sets that global, it must set __grafana_build_path__ too, and this key changes.
 */
export const iconCacheKey = (key: string) => `public/build/img/icons/${key}.svg`;

export function fillIconCache(cache: { set(url: string, item: StorageItem): void }, icons: Record<string, string>) {
  for (const [key, content] of Object.entries(icons)) {
    cache.set(iconCacheKey(key), { content, status: 'loaded' });
  }
}
