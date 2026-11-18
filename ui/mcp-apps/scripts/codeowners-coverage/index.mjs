import enquirer from 'enquirer';
import { execFileSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createManifest, splitCoverage, ownerSources } from './coverage.mjs';

const args = process.argv.slice(2);
const run = args[0] === '--run';
let selectedOwner = run ? args[1] : undefined;
if ((run && (args.length > 2 || selectedOwner?.startsWith('-'))) || (!run && args.length)) {
  throw new Error('Usage: npm run test:coverage:by-codeowner -- [@org/team]');
}

const require = createRequire(import.meta.url);
const packageRoot = fileURLToPath(new URL('../../', import.meta.url));
const repoRoot = path.resolve(packageRoot, '../..');
const output = path.join(packageRoot, 'coverage/by-codeowner');
const audit = execFileSync(process.execPath, [require.resolve('github-codeowners/dist/cli.js'),
  'audit', '--dir', repoRoot, '--codeowners', path.join(repoRoot, 'CODEOWNERS'), '--only-git', '--output', 'jsonl'],
{ cwd: repoRoot, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
const manifest = createManifest(audit.trim().split('\n').filter(Boolean).map(line => JSON.parse(line)));
if (run) {
  if (!selectedOwner) {
    if (!process.stdin.isTTY || !process.stdout.isTTY || process.env.CI === 'true') {
      throw new Error('Team selection requires an interactive terminal. Pass a codeowner: npm run test:coverage:by-codeowner -- @org/team');
    }
    const choices = manifest.owners.filter(owner => owner.startsWith('@grafana/'));
    if (!choices.length) throw new Error('No Grafana teams found in the ownership manifest');
    try {
      selectedOwner = await new enquirer.AutoComplete({
        name: 'codeowner',
        message: 'Select your team to report coverage by codeowner',
        limit: 10,
        choices,
      }).run();
    } catch {
      console.error('Team selection cancelled.');
      process.exit(130);
    }
  }
  const files = ownerSources(manifest, selectedOwner);
  execFileSync(process.execPath, [path.join(path.dirname(require.resolve('vitest/package.json')), 'vitest.mjs'), 'run', '--coverage',
    ...files.map(file => `--coverage.include=${path.relative(packageRoot, path.join(repoRoot, file)).replaceAll('\\', '/')}`)],
  { cwd: packageRoot, stdio: 'inherit' });
}
const coverage = JSON.parse(await readFile(path.join(packageRoot, 'coverage/coverage-summary.json'), 'utf8'));
const reports = splitCoverage(coverage, manifest.byFile, repoRoot);
await mkdir(output, { recursive: true });
const write = (name, data) => writeFile(path.join(output, name), JSON.stringify(data, null, 2) + '\n');
await write('teams.json', manifest.owners);
await write('teams-by-filename.json', manifest.byFile);
await write('filenames-by-team.json', manifest.byOwner);
const owners = [];
for (const owner of (run ? [selectedOwner] : Object.keys(reports).sort())) {
  const filename = `${encodeURIComponent(owner)}.json`;
  await write(filename, reports[owner]);
  owners.push({ owner, filename });
  console.log(`${owner}: ${reports[owner].summary.lines.pct ?? 'N/A'}% lines (${reports[owner].files.length} files)`);
}
await write('index.json', {
  commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repoRoot, encoding: 'utf8' }).trim(),
  package: '@mcp-grafana/mcp-apps',
  owners,
});
