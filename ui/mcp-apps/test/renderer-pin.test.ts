// @vitest-environment node
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { satisfies } from 'semver';
import { describe, expect, it } from 'vitest';
import { parsePin, sha256, tagVersion } from '../scripts/renderer.mjs';

// renderer/RENDERER pins a grafana/dataViz release. scripts/renderer.mjs downloads its files (test/renderer.global-setup.ts)
// and fails on a file that is not its pin, so these tests read the pinned release.
const dir = join(__dirname, '..', 'renderer');
const read = (name: string) => readFileSync(join(dir, name), 'utf8');

const pin = parsePin(read('RENDERER'));
const peers: Record<string, string> = JSON.parse(read('package.json')).peerDependencies;
const manifest = JSON.parse(read('renderer.manifest.json'));

describe('Renderer release', () => {
  it('is the bundle of the pin', () => {
    for (const [name, want] of Object.entries(pin.files)) {
      expect(sha256(readFileSync(join(dir, name))), name).toBe(want);
    }
  });

  it('has the manifest of its bundle (delivery.6)', () => {
    expect(manifest.sha256).toBe(pin.files['renderer.js']);
    expect(manifest.version).toBe(tagVersion(pin.tag));
  });

  it('has the license file that the bundle banner names', () => {
    expect(read('renderer.js').startsWith('/*! For license information please see renderer.js.LICENSE.txt */')).toBe(
      true,
    );
    expect(pin.files['renderer.js.LICENSE.txt']).toBeDefined();
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
