import type { Plugin } from 'vite';

/** Global that holds the packages the App shares with the Renderer resource (#1589). */
export const SHARED_MODULES_GLOBAL = '__grafanaMcpAppModules';

/**
 * The one list of shared packages. The App puts each on the global (sharedModulesPlugin), and the Renderer resource
 * takes each from it instead of bundling a copy (sharedModulesExternal). Code on both sides imports them as usual.
 */
export const SHARED_MODULE_IDS = ['react', 'react-dom', 'react-dom/client', 'react/jsx-runtime', '@emotion/css'] as const;

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

const sharedPackages = new Set(SHARED_MODULE_IDS.map((id) => packageOf(`node_modules/${id}`)));

/** The bundled modules that come from a shared package, at any depth of the import graph. */
export const sharedPackageCopies = (moduleIds: Iterable<string>) =>
  [...moduleIds].filter((id) => sharedPackages.has(packageOf(id)));

/**
 * Fails the build if the bundle holds a copy of a shared package, for example from a subpath such as
 * `react-dom/server` that a bundled dependency imports, or reads a shared package that is not on the list.
 */
export function sharedModulesGuard(): Plugin {
  return {
    name: 'grafana-shared-modules-guard',
    generateBundle(_options, bundle) {
      for (const chunk of Object.values(bundle)) {
        if (chunk.type !== 'chunk') continue;
        const copies = sharedPackageCopies(chunk.moduleIds);
        if (copies.length) this.error(`${chunk.fileName} bundles shared packages: ${copies.join(', ')}`);
        const unknown = chunk.imports.filter((id) => !shared.has(id));
        if (unknown.length) this.error(`${chunk.fileName} imports packages not on the global: ${unknown.join(', ')}`);
      }
    },
  };
}
