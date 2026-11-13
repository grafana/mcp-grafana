// @vitest-environment node
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

// The Renderer is a copy of a grafana/dataViz build. scripts/sync-viz-renderer.sh writes it and its pin RENDERER.
const dir = join(__dirname, '..', 'renderer');
const read = (name: string) => readFileSync(join(dir, name), 'utf8');

const pin = Object.fromEntries(
  read('RENDERER')
    .split('\n')
    .filter((line) => line.includes(': '))
    .map((line) => [line.slice(0, line.indexOf(': ')), line.slice(line.indexOf(': ') + 2)]),
);
const manifest = JSON.parse(read('renderer.manifest.json'));
const sha256 = createHash('sha256').update(readFileSync(join(dir, 'renderer.js'))).digest('hex');

describe('Renderer copy', () => {
  it('is the bundle of the pin', () => {
    expect(sha256).toBe(pin['sha256 renderer.js']);
  });

  it('has the manifest of its bundle (delivery.6)', () => {
    expect(manifest.sha256).toBe(sha256);
    expect(manifest.version).toBe(pin.version);
    expect(manifest.contractVersion).toBe(pin.contractVersion);
  });

  it('needs only the peers of renderer.9', () => {
    const peers = JSON.parse(read('package.json')).peerDependencies;
    expect(Object.keys(peers).sort()).toEqual(['@emotion/css', '@grafana/data', '@grafana/ui', 'react', 'react-dom']);
  });
});
