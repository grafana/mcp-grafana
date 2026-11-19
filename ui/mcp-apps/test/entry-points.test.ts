import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { forbiddenImports, LIBRARY_ENTRIES, SHELL_ENTRY } from '../entryPoints.config';
import packageJson from '../package.json';
import * as shell from '../src';

const root = join(__dirname, '..');

describe('library entry points', () => {
  it('exports only the shell from the package root', () => {
    expect(Object.keys(shell).sort()).toEqual(['McpAppSection', 'McpAppShell']);
  });

  it('has a package export for each entry point', () => {
    const exported = packageJson.exports as Record<string, unknown>;
    for (const [name, source] of Object.entries(LIBRARY_ENTRIES)) {
      const key = name === SHELL_ENTRY ? '.' : `./${name}`;
      expect(exported[key]).toEqual({ types: `./dist/${name}.d.ts`, import: `./dist/${name}.js` });
      expect(existsSync(join(root, source))).toBe(true);
    }
  });

  it('keeps ECharts out of the dependencies that each consumer installs', () => {
    expect(Object.keys(packageJson.dependencies)).not.toContain('echarts');
    expect(packageJson.peerDependencies).toHaveProperty('echarts');
    expect(packageJson.peerDependenciesMeta.echarts).toEqual({ optional: true });
  });

  it('does not publish the bundles that the Go server embeds', () => {
    for (const file of ['app.html', 'viz-renderer.js', 'viz-renderer.hash']) {
      expect(packageJson.files).toContain(`!dist/${file}`);
    }
  });
});

describe('forbiddenImports', () => {
  const forbidden = ['echarts'];

  it('finds a forbidden package that the entry imports', () => {
    const chunks = [{ fileName: 'index.js', imports: ['react', 'echarts/core'] }];
    expect(forbiddenImports(chunks, 'index.js', forbidden)).toEqual(['echarts/core']);
  });

  it('finds a forbidden package in a chunk that the entry imports', () => {
    const chunks = [
      { fileName: 'index.js', imports: ['shared.js'] },
      { fileName: 'shared.js', imports: ['echarts', 'index.js'] },
    ];
    expect(forbiddenImports(chunks, 'index.js', forbidden)).toEqual(['echarts']);
  });

  it('ignores the imports of a chunk that the entry does not load', () => {
    const chunks = [
      { fileName: 'index.js', imports: ['shared.js', '@emotion/css'] },
      { fileName: 'shared.js', imports: ['react'] },
      { fileName: 'experimental.js', imports: ['shared.js', 'echarts/charts'] },
    ];
    expect(forbiddenImports(chunks, 'index.js', forbidden)).toEqual([]);
  });

  it('does not match a package whose name only starts with a forbidden name', () => {
    const chunks = [{ fileName: 'index.js', imports: ['echarts-for-react'] }];
    expect(forbiddenImports(chunks, 'index.js', forbidden)).toEqual([]);
  });
});
