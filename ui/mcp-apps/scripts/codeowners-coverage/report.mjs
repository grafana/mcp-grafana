import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const args = process.argv.slice(2);
if (args.some(arg => arg !== '--dry-run')) throw new Error('Usage: report.mjs [--dry-run]');
const dryRun = args.includes('--dry-run');
const directory = new URL('../../coverage/by-codeowner/', import.meta.url);
const index = JSON.parse(await readFile(new URL('index.json', directory), 'utf8'));
if (!index.owners.length) throw new Error('No owner reports to publish');
for (const { owner, filename } of index.owners) {
  execFileSync('grafana-bench', ['report', '--report-input', 'jscoverage', '--report-output', 'log',
    ...(dryRun ? [] : ['--prometheus-metrics']), '--service', 'jscoverage', '--service-version', index.commit,
    '--suite-name', 'grafana/mcp-grafana', '--js-coverage-codeowner', owner,
    '--js-coverage-package', index.package, '--run-stage', 'ci',
    path.join(fileURLToPath(directory), filename)], { stdio: 'inherit' });
}
