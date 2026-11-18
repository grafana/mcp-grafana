import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { createManifest, splitCoverage, ownerSources } from './coverage.mjs';

const require = createRequire(import.meta.url);
const counts = (total, covered) => Object.fromEntries(
  ['lines', 'statements', 'functions', 'branches'].map(key => [key, { total, covered }])
);

test('weights counts, credits shared files to both owners, and preserves measured zero', () => {
  const manifest = createManifest([
    { path: 'a.ts', owners: ['@org/a', '@org/b', '@org/a'] },
    { path: 'b.ts', owners: ['@org/a'] },
    { path: 'c.ts', owners: [] },
  ]);
  const reports = splitCoverage({
    total: counts(1000, 999), '/repo/a.ts': counts(2, 2),
    '/repo/b.ts': counts(8, 0), '/repo/c.ts': counts(0, 0),
  }, manifest.byFile, '/repo');
  assert.deepEqual(reports['@org/a'].summary.lines, { total: 10, covered: 2, pct: 20 });
  assert.deepEqual(reports['@org/b'].summary.lines, { total: 2, covered: 2, pct: 100 });
  assert.equal(reports.unowned.summary.lines.pct, null);
  assert.deepEqual(manifest.byOwner['@org/a'], ['a.ts', 'b.ts']);
  assert.deepEqual(reports['@org/a'].files, ['a.ts', 'b.ts']);
});

test('rejects missing ownership, empty coverage, and invalid counts', () => {
  assert.throws(() => splitCoverage({ '/repo/a.ts': counts(1, 0) }, {}, '/repo'), /missing from ownership/);
  assert.throws(() => splitCoverage({ total: counts(0, 0) }, {}, '/repo'), /No source coverage/);
  assert.throws(() => splitCoverage({ '/repo/a.ts': counts(1, 2) }, { 'a.ts': [] }, '/repo'), /Invalid lines/);
});

test('audits root CODEOWNERS with last-match precedence and joint ownership', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'coverage-owners-'));
  try {
    execFileSync('git', ['init', '-q', root]);
    writeFileSync(path.join(root, 'CODEOWNERS'), '* @org/default\n/shared.ts @org/a @org/b\n/private.ts @org/override\n');
    for (const file of ['shared.ts', 'private.ts', 'other.ts']) writeFileSync(path.join(root, file), 'export {};\n');
    execFileSync('git', ['add', '.'], { cwd: root });
    const raw = execFileSync(process.execPath, [require.resolve('github-codeowners/dist/cli.js'),
      'audit', '--dir', root, '--codeowners', path.join(root, 'CODEOWNERS'), '--only-git', '--output', 'jsonl'],
    { encoding: 'utf8', cwd: root });
    const manifest = createManifest(raw.trim().split('\n').map(line => JSON.parse(line)));
    assert.deepEqual(manifest.byFile['shared.ts'], ['@org/a', '@org/b']);
    assert.deepEqual(manifest.byFile['private.ts'], ['@org/override']);
    assert.deepEqual(manifest.byFile['other.ts'], ['@org/default']);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('selects only owned production sources, including jointly owned files', () => {
  const manifest = createManifest([
    { path: 'ui/mcp-apps/app/shared.ts', owners: ['@org/a', '@org/b'] },
    { path: 'ui/mcp-apps/src/other.tsx', owners: ['@org/b'] },
    { path: 'ui/mcp-apps/app/types.d.ts', owners: ['@org/a'] },
    { path: 'ui/mcp-apps/test/shared.test.ts', owners: ['@org/a', '@org/tests'] },
    { path: 'ui/panel-viewer/src/app.ts', owners: ['@org/a'] },
  ]);
  assert.deepEqual(ownerSources(manifest, '@org/a'), ['ui/mcp-apps/app/shared.ts']);
  assert.throws(() => ownerSources(manifest, '@org/missing'), /Unknown codeowner/);
  assert.throws(() => ownerSources(manifest, '@org/tests'), /No coverage source/);
});
