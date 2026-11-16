// @vitest-environment node
import { createHash } from 'node:crypto';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { satisfies } from 'semver';
import { describe, expect, it } from 'vitest';

// The Renderer is a copy of a grafana/dataViz release. scripts/sync-viz-renderer.sh writes it and its pin RENDERER.
const dir = join(__dirname, '..', 'renderer');
const read = (name: string) => readFileSync(join(dir, name), 'utf8');

const pin = Object.fromEntries(
  read('RENDERER')
    .split('\n')
    .filter((line) => line.includes(': '))
    .map((line) => [line.slice(0, line.indexOf(': ')), line.slice(line.indexOf(': ') + 2)]),
);
const peers: Record<string, string> = JSON.parse(read('package.json')).peerDependencies;
const manifest = JSON.parse(read('renderer.manifest.json'));
const hash = (name: string) => createHash('sha256').update(readFileSync(join(dir, name))).digest('hex');
const sha256 = hash('renderer.js');

// The files of the release, from the pin lines `sha256 <file>: <hash>`.
const pinned = Object.keys(pin)
  .filter((key) => key.startsWith('sha256 '))
  .map((key) => key.slice('sha256 '.length));

describe('Renderer copy', () => {
  it('holds exactly the files of the pin', () => {
    const files = readdirSync(dir).filter((name) => name !== 'RENDERER' && name !== 'README.md');
    expect(files.sort()).toEqual([...pinned].sort());
    expect(pinned).toEqual(expect.arrayContaining(['renderer.js', 'icons.json', 'renderer.manifest.json']));
  });

  // delivery.9 for icons.json.
  it.each(pinned)('has the %s of the pin', (name) => {
    expect(hash(name)).toBe(pin[`sha256 ${name}`]);
  });

  it('has the manifest of its bundle (delivery.6)', () => {
    expect(manifest.sha256).toBe(sha256);
    expect(manifest.version).toBe(pin.version);
    expect(manifest.contractVersion).toBe(pin.contractVersion);
  });

  it('is the release of its version', () => {
    expect(pin.repository).toBe('grafana/dataViz');
    expect(pin.release).toBe(`grafana-echarts-renderer/v${pin.version}`);
    expect(pin.commit).toMatch(/^[0-9a-f]{40}$/);
    expect(JSON.parse(read('package.json')).version).toBe(pin.version);
  });

  it('needs only the peers of renderer.9', () => {
    expect(Object.keys(peers).sort()).toEqual(['@emotion/css', '@grafana/data', '@grafana/ui', 'react', 'react-dom']);
  });

  // A peer below the Renderer range breaks only at run time, in the sandbox iframe.
  it.each(Object.entries(peers))('gets an installed %s in its range %s', (name, range) => {
    const { version } = JSON.parse(readFileSync(join(__dirname, '..', 'node_modules', name, 'package.json'), 'utf8'));
    expect(satisfies(version, range), `${name} ${version}`).toBe(true);
  });
});
