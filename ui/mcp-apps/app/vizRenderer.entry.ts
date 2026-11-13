// Entry of the Renderer resource, dist/viz-renderer.js (#1589). It bundles the Renderer with its own @grafana/ui,
// @grafana/data and react-inlinesvg, and takes the shared packages from the App's global (sharedModules.ts). The
// icon cache fill lives here, because it must reach the react-inlinesvg that this bundle's @grafana/ui reads.
import { cacheStore } from 'react-inlinesvg';
import icons from '../renderer/icons.json';
import { fillIconCache } from './iconCache';

fillIconCache(cacheStore, icons);

export * from '../renderer/renderer.js';
