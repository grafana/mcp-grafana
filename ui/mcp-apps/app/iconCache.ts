import type { StorageItem } from 'react-inlinesvg';

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
