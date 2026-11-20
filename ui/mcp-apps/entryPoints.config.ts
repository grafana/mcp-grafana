import type { Plugin } from 'vite';

/** The entry points of the published library (#1649). Each key is a file of dist/ and an export of package.json. */
export const LIBRARY_ENTRIES = {
  index: 'src/index.ts',
  experimental: 'src/experimental.ts',
} as const;

/** The entry point that has only the shell. */
export const SHELL_ENTRY = 'index';

/** The packages that the shell entry point must not load. `echarts` is an optional peer dependency. */
export const SHELL_FORBIDDEN_PACKAGES = ['echarts'];

/** The part of a Rollup output chunk that the guard reads. */
export interface ChunkImports {
  fileName: string;
  imports: string[];
}

const isPackage = (id: string, name: string) => id === name || id.startsWith(`${name}/`);

/**
 * The forbidden packages that a chunk loads, in the chunk or in a chunk that it imports. `imports` of a chunk has
 * the file names of other chunks and the ids of external packages.
 */
export function forbiddenImports(chunks: ChunkImports[], entryFile: string, forbidden: string[]): string[] {
  const byFile = new Map(chunks.map((chunk) => [chunk.fileName, chunk]));
  const seen = new Set<string>();
  const found = new Set<string>();
  const visit = (fileName: string) => {
    if (seen.has(fileName)) return;
    seen.add(fileName);
    for (const id of byFile.get(fileName)?.imports ?? []) {
      if (byFile.has(id)) visit(id);
      else if (forbidden.some((name) => isPackage(id, name))) found.add(id);
    }
  };
  visit(entryFile);
  return [...found].sort();
}

/** Fails the library build if the shell entry point loads a forbidden package. */
export function shellEntryGuard(): Plugin {
  return {
    name: 'grafana-shell-entry-guard',
    generateBundle(_options, bundle) {
      const chunks = Object.values(bundle).filter((output) => output.type === 'chunk');
      const entryFile = `${SHELL_ENTRY}.js`;
      if (!chunks.some((chunk) => chunk.fileName === entryFile)) this.error(`The build has no ${entryFile}`);
      const found = forbiddenImports(chunks, entryFile, SHELL_FORBIDDEN_PACKAGES);
      if (found.length) this.error(`${entryFile} loads packages that only the experimental entry can load: ${found.join(', ')}`);
    },
  };
}
