import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';

// The fixtures of @grafana/viz-contract, at the version in package.json.
const root = join(dirname(createRequire(import.meta.url).resolve('@grafana/viz-contract/package.json')), 'fixtures');

export const readVizFixture = (set: string, file: string) => JSON.parse(readFileSync(join(root, set, file), 'utf8'));

// The App sets have arguments, a result and a viz spec (fixtures.9).
export const vizAppFixtureSets = readdirSync(root).filter((set) =>
  ['args.json', 'result.json', 'spec.json'].every((file) => existsSync(join(root, set, file)))
);
