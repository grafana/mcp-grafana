// Names that the Renderer resource build, the App build and the App code share (#1589). No Node imports here: the
// App bundles this file.

/** The global that the Renderer resource sets to the Renderer module when it runs as an inline script. */
export const VIZ_RENDERER_GLOBAL = '__grafanaVizRenderer';

/** The file of the Renderer resource in dist/. */
export const VIZ_RENDERER_FILE = 'viz-renderer.js';

/** The file in dist/ that holds the first 16 hex digits of the sha256 of VIZ_RENDERER_FILE. */
export const VIZ_RENDERER_HASH_FILE = 'viz-renderer.hash';

/** The URI of the Renderer resource. vizRendererURI in ui_grafana.go is the same one. */
export const vizRendererUri = (hash: string) => `ui://grafana/viz-renderer-${hash.trim()}.js`;
