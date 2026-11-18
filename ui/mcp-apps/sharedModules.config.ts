import type { Plugin } from 'vite';

/** Global that holds the packages the App shares with the Renderer resource (#1589). */
export const SHARED_MODULES_GLOBAL = '__grafanaMcpAppModules';

/**
 * The one list of shared packages. The App puts each on the global (sharedModulesPlugin), and the Renderer resource
 * takes each from it instead of bundling a copy (sharedModulesExternal). Code on both sides imports them as usual.
 */
export const SHARED_MODULE_IDS = [
  'react',
  'react-dom',
  'react-dom/client',
  'react/jsx-runtime',
  '@emotion/css',
  '@emotion/react',
] as const;

const VIRTUAL_ID = 'virtual:shared-modules';
const RESOLVED_ID = `\0${VIRTUAL_ID}`;

/** Generates virtual:shared-modules for the App: the global name, and a frozen object of the shared packages. */
export function sharedModulesPlugin(): Plugin {
  return {
    name: 'grafana-shared-modules',
    resolveId: (id) => (id === VIRTUAL_ID ? RESOLVED_ID : undefined),
    load(id) {
      if (id !== RESOLVED_ID) return;
      return [
        ...SHARED_MODULE_IDS.map((pkg, i) => `import * as m${i} from ${JSON.stringify(pkg)};`),
        `export const SHARED_MODULES_GLOBAL = ${JSON.stringify(SHARED_MODULES_GLOBAL)};`,
        `export const sharedModules = Object.freeze({ ${SHARED_MODULE_IDS.map((pkg, i) => `${JSON.stringify(pkg)}: m${i}`).join(', ')} });`,
      ].join('\n');
    },
  };
}

const shared = new Set<string>(SHARED_MODULE_IDS);

/** Rollup options for a bundle that reads the shared packages from the global. */
export const sharedModulesExternal = {
  external: (id: string) => shared.has(id),
  globals: (id: string) => `${SHARED_MODULES_GLOBAL}[${JSON.stringify(id)}]`,
};

/** The npm package of a resolved module path, for example `@emotion/css` or `lodash.debounce`. */
export function packageOf(moduleId: string): string | undefined {
  const i = moduleId.lastIndexOf('node_modules/');
  if (i < 0) return undefined;
  const parts = moduleId.slice(i + 'node_modules/'.length).split('/');
  return parts[0].startsWith('@') ? `${parts[0]}/${parts[1]}` : parts[0];
}

// @emotion/cache is not shared, but a bundled copy is a second Emotion cache, so the guard fails on it too.
const guardedPackages = new Set([...SHARED_MODULE_IDS.map((id) => packageOf(`node_modules/${id}`)), '@emotion/cache']);

/** The bundled modules that come from a shared package or @emotion/cache, at any depth of the import graph. */
export const sharedPackageCopies = (moduleIds: Iterable<string>) =>
  [...moduleIds].filter((id) => guardedPackages.has(packageOf(id)));

/**
 * Fails the build if the bundle holds a copy of a shared package, for example from a subpath such as
 * `react-dom/server` that a bundled dependency imports. Only listed packages are external, so the bundle reads no
 * other package from the global.
 */
export function sharedModulesGuard(): Plugin {
  return {
    name: 'grafana-shared-modules-guard',
    generateBundle(_options, bundle) {
      for (const chunk of Object.values(bundle)) {
        if (chunk.type !== 'chunk') continue;
        const copies = sharedPackageCopies(chunk.moduleIds);
        if (copies.length) this.error(`${chunk.fileName} bundles shared packages: ${copies.join(', ')}`);
      }
    },
  };
}

/**
 * The copies of an npm package in a bundle: one per installed directory, and one more for each directory whose
 * CommonJS build (a .cjs file, as react-inlinesvg ships) is bundled next to its ES module build, because each build
 * has its own module state.
 */
export function packageCopies(moduleIds: Iterable<string>, pkg: string): string[] {
  const copies = new Set<string>();
  for (const raw of moduleIds) {
    const id = raw.replace(/^\0/, '').replace(/\?.*$/, '');
    if (packageOf(id) !== pkg) continue;
    const dir = id.slice(0, id.lastIndexOf(`node_modules/${pkg}`) + `node_modules/${pkg}`.length);
    copies.add(/\.c[jt]s$/.test(id) ? `${dir} (CommonJS)` : dir);
  }
  return [...copies].sort();
}

/**
 * Fails the build if the bundle has more than one copy of react-inlinesvg (delivery.10). With two, the icon cache that
 * the bundle fills (renderer.15) can be another one than the cache that Icon of @grafana/ui reads.
 */
export function iconCacheGuard(): Plugin {
  return {
    name: 'grafana-icon-cache-guard',
    generateBundle(_options, bundle) {
      const ids = Object.values(bundle).flatMap((chunk) => (chunk.type === 'chunk' ? chunk.moduleIds : []));
      const copies = packageCopies(ids, 'react-inlinesvg');
      if (copies.length > 1) {
        this.error(`the bundle has ${copies.length} copies of react-inlinesvg: ${copies.join(', ')}`);
      }
    },
  };
}
