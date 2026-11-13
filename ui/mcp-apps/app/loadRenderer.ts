import { fillIconCache } from './iconCache';

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
