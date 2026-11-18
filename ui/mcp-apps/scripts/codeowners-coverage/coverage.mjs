import path from 'node:path';

const metrics = ['lines', 'statements', 'functions', 'branches'];

// Keep the manifest shape compatible with Grafana's codeowners-manifest consumers.
export function createManifest(audit) {
  const byFile = {};
  const byOwner = {};
  for (const { path: file, owners } of audit) {
    const normalized = file.replaceAll('\\', '/').replace(/^\.\//, '');
    byFile[normalized] = [...new Set(owners)].sort();
    for (const owner of byFile[normalized]) (byOwner[owner] ??= []).push(normalized);
  }
  for (const files of Object.values(byOwner)) files.sort();
  return { byFile, byOwner, owners: Object.keys(byOwner).sort() };
}

export function splitCoverage(coverage, byFile, repoRoot) {
  const reports = {};
  for (const [filename, summary] of Object.entries(coverage)) {
    if (filename === 'total') continue;
    const file = path.relative(repoRoot, path.resolve(repoRoot, filename)).replaceAll('\\', '/');
    if (!Object.hasOwn(byFile, file)) throw new Error(`Coverage file missing from ownership manifest: ${file}`);
    const owners = byFile[file].length ? byFile[file] : ['unowned'];
    for (const owner of new Set(owners)) {
      const report = reports[owner] ??= { summary: {}, files: [] };
      report.files.push(file);
      for (const metric of metrics) {
        const value = summary[metric];
        if (!value || !Number.isInteger(value.total) || !Number.isInteger(value.covered) ||
            value.total < 0 || value.covered < 0 || value.covered > value.total) {
          throw new Error(`Invalid ${metric} coverage for ${file}`);
        }
        const aggregate = report.summary[metric] ??= { total: 0, covered: 0 };
        aggregate.total += value.total;
        aggregate.covered += value.covered;
      }
    }
  }
  if (!Object.keys(reports).length) throw new Error('No source coverage found');
  for (const report of Object.values(reports)) {
    report.files.sort();
    for (const value of Object.values(report.summary)) {
      // Bench ignores null percentages: no executable items is not 100% coverage.
      value.pct = value.total ? Math.floor(value.covered / value.total * 10000) / 100 : null;
    }
  }
  return reports;
}

export function ownerSources(manifest, owner) {
  if (!Object.hasOwn(manifest.byOwner, owner)) throw new Error(`Unknown codeowner: ${owner}`);
  const files = manifest.byOwner[owner].filter(file =>
    /^ui\/mcp-apps\/(src|app)\/.*\.tsx?$/.test(file) && !file.endsWith('.d.ts'));
  if (!files.length) throw new Error(`No coverage source files for codeowner: ${owner}`);
  return files;
}
