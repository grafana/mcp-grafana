// @vitest-environment node
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  PROVENANCE,
  REQUIRED_FILES,
  fetchRenderer,
  formatPin,
  parsePin,
  sha256,
  verifyFile,
  type Pin,
} from '../scripts/renderer.mjs';

const content = Object.fromEntries(REQUIRED_FILES.map((name) => [name, `content of ${name}`]));
const pin: Pin = {
  repository: 'grafana/dataViz',
  tag: 'grafana-echarts-renderer/v1.2.3',
  signer: 'https://github.com/grafana/dataViz/.github/workflows/release-please.yml@refs/heads/main',
  files: Object.fromEntries(REQUIRED_FILES.map((name) => [name, sha256(content[name])])),
};

const served = (body: Record<string, string>) =>
  vi.fn(async (url: string) => {
    const name = url.slice(url.lastIndexOf('/') + 1);
    return name in body ? new Response(body[name]) : new Response('not found', { status: 404 });
  });

let dir: string;
beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), 'renderer-'));
  writeFileSync(join(dir, 'RENDERER'), formatPin(pin));
});
afterEach(() => rmSync(dir, { recursive: true, force: true }));

describe('pin', () => {
  it('round-trips', () => {
    expect(parsePin(formatPin(pin))).toEqual(pin);
  });

  it('rejects a file name with a directory part', () => {
    const text = formatPin(pin).replace('sha256 icons.json', 'sha256 ../icons.json');
    expect(() => parsePin(text)).toThrow(/bad file name/);
  });

  it('rejects a tag that is not a Renderer release', () => {
    expect(() => parsePin(formatPin({ ...pin, tag: 'grafana-relations-panel/v1.0.0' }))).toThrow(/release tag/);
  });
});

describe('fetchRenderer', () => {
  it('downloads the pinned files from the release', async () => {
    const fetchImpl = served(content);
    const { downloaded } = await fetchRenderer({ dir, fetchImpl });
    expect(downloaded.sort()).toEqual([...REQUIRED_FILES].sort());
    expect(fetchImpl).toHaveBeenCalledWith(
      'https://github.com/grafana/dataViz/releases/download/grafana-echarts-renderer/v1.2.3/renderer.js',
      expect.anything(),
    );
    expect(readFileSync(join(dir, 'renderer.js'), 'utf8')).toBe(content['renderer.js']);
  });

  it('makes no request when the files on disk match the pin', async () => {
    for (const name of REQUIRED_FILES) writeFileSync(join(dir, name), content[name]);
    const fetchImpl = served({});
    expect((await fetchRenderer({ dir, fetchImpl })).downloaded).toEqual([]);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('replaces a file on disk that is not its pin', async () => {
    writeFileSync(join(dir, 'icons.json'), 'an old build');
    await fetchRenderer({ dir, fetchImpl: served(content) });
    expect(readFileSync(join(dir, 'icons.json'), 'utf8')).toBe(content['icons.json']);
  });

  it('fails on a download that is not its pin and leaves no file', async () => {
    await expect(fetchRenderer({ dir, fetchImpl: served({ ...content, 'renderer.js': 'tampered' }) })).rejects.toThrow(
      /renderer\.js: sha256 .* is not the pin/,
    );
    expect(existsSync(join(dir, 'renderer.js'))).toBe(false);
    expect(existsSync(join(dir, 'renderer.js.download'))).toBe(false);
  });

  it('fails on a missing release file', async () => {
    const { ['icons.json']: _, ...partial } = content;
    await expect(fetchRenderer({ dir, fetchImpl: served(partial) })).rejects.toThrow(/HTTP 404/);
  });
});

describe('verifyFile', () => {
  const data = Buffer.from('content of renderer.js');
  const bundle = (digest: string, predicateType = PROVENANCE) => ({
    dsseEnvelope: {
      payloadType: 'application/vnd.in-toto+json',
      payload: Buffer.from(JSON.stringify({ predicateType, subject: [{ digest: { sha256: digest } }] })).toString(
        'base64',
      ),
    },
  });
  const api = (attestations: unknown[]) => vi.fn(async () => Response.json({ attestations }));

  it('passes a provenance attestation of the file that the verifier accepts', async () => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = api([{ bundle: bundle(sha256(data)) }]);
    await verifyFile('renderer.js', data, { ...pin, verifier, fetchImpl, token: 't0ken' });
    expect(verifier.verify).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledWith(
      `https://api.github.com/repos/grafana/dataViz/attestations/sha256:${sha256(data)}`,
      expect.objectContaining({ headers: expect.objectContaining({ Authorization: 'Bearer t0ken' }) }),
    );
  });

  it('fails when the statement subject is another file, even with a valid signature', async () => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = api([{ bundle: bundle(sha256('another file')) }]);
    await expect(verifyFile('renderer.js', data, { ...pin, verifier, fetchImpl })).rejects.toThrow(/subject digest/);
    expect(verifier.verify).not.toHaveBeenCalled();
  });

  it('ignores attestations that are not provenance', async () => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = api([{ bundle: bundle(sha256(data), 'https://in-toto.io/attestation/release/v0.2') }]);
    await expect(verifyFile('renderer.js', data, { ...pin, verifier, fetchImpl })).rejects.toThrow(
      /no https:\/\/slsa\.dev\/provenance\/v1 attestation/,
    );
  });

  it('fails when the verifier rejects the signer', async () => {
    const verifier = {
      verify: vi.fn(() => {
        throw new Error('certificate identity error');
      }),
    };
    const fetchImpl = api([{ bundle: bundle(sha256(data)) }]);
    await expect(verifyFile('renderer.js', data, { ...pin, verifier, fetchImpl })).rejects.toThrow(
      /certificate identity error/,
    );
  });
});
