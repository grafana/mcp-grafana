// @vitest-environment node
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  PROVENANCE,
  REPOSITORY,
  REQUIRED_FILES,
  SIGNER,
  bumpRenderer,
  fetchRenderer,
  formatPin,
  parsePin,
  sha256,
  verifyFile,
  verifyRenderer,
  type Pin,
} from '../scripts/renderer.mjs';

const TAG = 'grafana-echarts-renderer/v1.2.3';
const COMMIT = 'a'.repeat(40);
const API = `https://api.github.com/repos/${REPOSITORY}`;

const baseContent: Record<string, string> = Object.fromEntries(
  REQUIRED_FILES.map((name) => [name, `content of ${name}`]),
);
const content: Record<string, string> = {
  ...baseContent,
  'renderer.manifest.json': JSON.stringify({
    version: '1.2.3',
    sha256: sha256(baseContent['renderer.js']),
    contractVersion: '1.2.0',
  }),
  'package.json': JSON.stringify({ name: '@plugins/grafana-echarts-renderer', version: '1.2.3' }),
};
const pin: Pin = {
  repository: REPOSITORY,
  tag: TAG,
  commit: COMMIT,
  signer: SIGNER,
  version: '1.2.3',
  contractVersion: '1.2.0',
  files: Object.fromEntries(REQUIRED_FILES.map((name) => [name, sha256(content[name])])),
};

/** A provenance bundle whose statement names `digest` and was built from `commit` on a `runner` runner. */
const bundle = (
  digest: string,
  { predicateType = PROVENANCE, commit = COMMIT, runner = 'github-hosted' as string | null } = {},
) => ({
  dsseEnvelope: {
    payloadType: 'application/vnd.in-toto+json',
    payload: Buffer.from(
      JSON.stringify({
        predicateType,
        subject: [{ digest: { sha256: digest } }],
        predicate: {
          buildDefinition: {
            resolvedDependencies: [
              { uri: `git+https://github.com/${REPOSITORY}@refs/heads/main`, digest: { gitCommit: commit } },
            ],
            // null leaves the runner out of the statement.
            internalParameters: { github: { event_name: 'push', runner_environment: runner ?? undefined } },
          },
        },
      }),
    ).toString('base64'),
  },
});

interface Release {
  files?: Record<string, string>;
  immutable?: boolean;
  commit?: string;
  builtFrom?: string;
  runner?: string;
}

/** A fake GitHub: release downloads, the releases and tag APIs, and the attestations API. */
function github({
  files = content,
  immutable = true,
  commit = COMMIT,
  builtFrom = COMMIT,
  runner = 'github-hosted',
}: Release = {}) {
  return vi.fn(async (url: string, _init?: RequestInit) => {
    if (url.startsWith(`https://github.com/${REPOSITORY}/releases/download/${TAG}/`)) {
      const name = url.slice(url.lastIndexOf('/') + 1);
      return name in files ? new Response(files[name]) : new Response('not found', { status: 404 });
    }
    if (url === `${API}/releases/tags/${TAG}`) {
      return Response.json({ immutable, assets: Object.keys(files).map((name) => ({ name })) });
    }
    if (url === `${API}/git/ref/tags/${TAG}`) return Response.json({ object: { type: 'commit', sha: commit } });
    const attestation = /\/attestations\/sha256:([0-9a-f]{64})$/.exec(url);
    if (attestation) {
      return Response.json({ attestations: [{ bundle: bundle(attestation[1], { commit: builtFrom, runner }) }] });
    }
    return new Response('not found', { status: 404 });
  });
}

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

  it('rejects a version that is not the tag version', () => {
    expect(() => parsePin(formatPin({ ...pin, version: '1.2.4' }))).toThrow(/not the version of tag/);
  });

  // The pin cannot choose what is trusted: a changed signer or repository fails instead of being trusted.
  it.each([
    ['signer', { signer: 'https://github.com/someone-else/repo/.github/workflows/release.yml@refs/heads/main' }],
    ['repository', { repository: 'someone-else/dataViz' }],
  ])('rejects another %s', (_, change) => {
    expect(() => parsePin(formatPin({ ...pin, ...change }))).toThrow(/not the trusted/);
  });
});

describe('fetchRenderer', () => {
  it('downloads the pinned files from the release', async () => {
    const fetchImpl = github();
    const { downloaded } = await fetchRenderer({ dir, fetchImpl, localDir: null });
    expect(downloaded.sort()).toEqual([...REQUIRED_FILES].sort());
    expect(fetchImpl).toHaveBeenCalledWith(
      `https://github.com/${REPOSITORY}/releases/download/${TAG}/renderer.js`,
      expect.anything(),
    );
    expect(readFileSync(join(dir, 'renderer.js'), 'utf8')).toBe(content['renderer.js']);
  });

  it('makes no request when the files on disk match the pin', async () => {
    for (const name of REQUIRED_FILES) writeFileSync(join(dir, name), content[name]);
    const fetchImpl = github();
    expect((await fetchRenderer({ dir, fetchImpl, localDir: null })).downloaded).toEqual([]);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('replaces a file on disk that is not its pin, and says how to keep a local build', async () => {
    writeFileSync(join(dir, 'icons.json'), 'an old build');
    const log = vi.fn();
    await fetchRenderer({ dir, fetchImpl: github(), log, localDir: null });
    expect(readFileSync(join(dir, 'icons.json'), 'utf8')).toBe(content['icons.json']);
    expect(log).toHaveBeenCalledWith(expect.stringMatching(/Replaced icons\.json .*RENDERER_LOCAL_DIR/));
  });

  it('fails on a download that is not its pin and leaves no file', async () => {
    const fetchImpl = github({ files: { ...content, 'renderer.js': 'tampered' } });
    await expect(fetchRenderer({ dir, fetchImpl, localDir: null })).rejects.toThrow(
      /renderer\.js: sha256 .* is not the pin/,
    );
    expect(existsSync(join(dir, 'renderer.js'))).toBe(false);
    expect(existsSync(join(dir, 'renderer.js.download'))).toBe(false);
  });

  it('fails on a missing release file', async () => {
    const { ['icons.json']: _, ...partial } = content;
    await expect(fetchRenderer({ dir, fetchImpl: github({ files: partial }), localDir: null })).rejects.toThrow(
      /HTTP 404/,
    );
  });

  it('copies a local build from localDir without a request or a hash check', async () => {
    const local = join(dir, 'local-build');
    mkdirSync(local);
    for (const name of REQUIRED_FILES) writeFileSync(join(local, name), `local ${name}`);
    const fetchImpl = github();
    const { local: copied } = await fetchRenderer({ dir, fetchImpl, localDir: local });
    expect(copied?.sort()).toEqual([...REQUIRED_FILES].sort());
    expect(readFileSync(join(dir, 'renderer.js'), 'utf8')).toBe('local renderer.js');
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('fails on a local build without a required file', async () => {
    const local = join(dir, 'local-build');
    mkdirSync(local);
    writeFileSync(join(local, 'renderer.js'), 'local');
    await expect(fetchRenderer({ dir, fetchImpl: github(), localDir: local })).rejects.toThrow(/has no/);
  });
});

describe('verifyFile', () => {
  const data = Buffer.from('content of renderer.js');
  const api = (attestations: unknown[]) => vi.fn(async () => Response.json({ attestations }));

  it('passes a provenance attestation of the file that the verifier accepts', async () => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = api([{ bundle: bundle(sha256(data)) }]);
    await verifyFile('renderer.js', data, { verifier, fetchImpl, token: 't0ken' });
    expect(verifier.verify).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledWith(
      `${API}/attestations/sha256:${sha256(data)}`,
      expect.objectContaining({ headers: expect.objectContaining({ Authorization: 'Bearer t0ken' }) }),
    );
  });

  it('fails when the statement subject is another file, even with a valid signature', async () => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = api([{ bundle: bundle(sha256('another file')) }]);
    await expect(verifyFile('renderer.js', data, { verifier, fetchImpl })).rejects.toThrow(/subject digest/);
    expect(verifier.verify).not.toHaveBeenCalled();
  });

  it('ignores attestations that are not provenance', async () => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = api([
      { bundle: bundle(sha256(data), { predicateType: 'https://in-toto.io/attestation/release/v0.2' }) },
    ]);
    await expect(verifyFile('renderer.js', data, { verifier, fetchImpl })).rejects.toThrow(
      /no https:\/\/slsa\.dev\/provenance\/v1 attestation/,
    );
  });

  it.each([['self-hosted'], [null]])('fails for a build on a %s runner, even with a valid signature', async (runner) => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = api([{ bundle: bundle(sha256(data), { runner }) }]);
    await expect(verifyFile('renderer.js', data, { verifier, fetchImpl })).rejects.toThrow(/not github-hosted/);
    expect(verifier.verify).not.toHaveBeenCalled();
  });

  // A file that is the same in several releases has one attestation per build, in no set order.
  it('passes the attestation of the expected commit among those of other builds', async () => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = api([
      { bundle: bundle(sha256(data), { commit: 'b'.repeat(40) }) },
      { bundle: bundle(sha256(data), { commit: COMMIT }) },
    ]);
    await verifyFile('renderer.js', data, { verifier, fetchImpl, commit: COMMIT });
    expect(verifier.verify).toHaveBeenCalledTimes(1);
  });

  it('fails when no attestation is of the expected commit', async () => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = api([{ bundle: bundle(sha256(data), { commit: 'b'.repeat(40) }) }]);
    await expect(verifyFile('renderer.js', data, { verifier, fetchImpl, commit: COMMIT })).rejects.toThrow(
      /built from commit b+, not a+/,
    );
    expect(verifier.verify).not.toHaveBeenCalled();
  });

  it('fails when the verifier rejects the signer', async () => {
    const verifier = {
      verify: vi.fn(() => {
        throw new Error('certificate identity error');
      }),
    };
    const fetchImpl = api([{ bundle: bundle(sha256(data)) }]);
    await expect(verifyFile('renderer.js', data, { verifier, fetchImpl })).rejects.toThrow(/certificate identity error/);
  });
});

describe('verifyRenderer', () => {
  it('fetches the pinned files and verifies each one', async () => {
    const verifier = { verify: vi.fn() };
    await verifyRenderer({ dir, fetchImpl: github(), verifier });
    expect(verifier.verify).toHaveBeenCalledTimes(REQUIRED_FILES.length);
  });

  it('verifies the pinned release, not a RENDERER_LOCAL_DIR build', async () => {
    const local = join(dir, 'local-build');
    mkdirSync(local);
    for (const name of REQUIRED_FILES) writeFileSync(join(local, name), `local ${name}`);
    vi.stubEnv('RENDERER_LOCAL_DIR', local);
    try {
      const log = vi.fn();
      await verifyRenderer({ dir, fetchImpl: github(), verifier: { verify: vi.fn() }, log });
      expect(readFileSync(join(dir, 'renderer.js'), 'utf8')).toBe(content['renderer.js']);
      expect(log).toHaveBeenCalledWith(expect.stringMatching(/Ignoring RENDERER_LOCAL_DIR/));
    } finally {
      vi.unstubAllEnvs();
    }
  });

  it('uses RENDERER_LOCAL_DIR by default for a fetch', async () => {
    const local = join(dir, 'local-build');
    mkdirSync(local);
    for (const name of REQUIRED_FILES) writeFileSync(join(local, name), `local ${name}`);
    vi.stubEnv('RENDERER_LOCAL_DIR', local);
    try {
      await fetchRenderer({ dir, fetchImpl: github() });
      expect(readFileSync(join(dir, 'renderer.js'), 'utf8')).toBe('local renderer.js');
    } finally {
      vi.unstubAllEnvs();
    }
  });

  it('fails when one file has no valid attestation', async () => {
    const verifier = { verify: vi.fn() };
    const fetchImpl = github();
    // The attestations API has nothing for icons.json.
    const icons = `sha256:${sha256(content['icons.json'])}`;
    const failing = vi.fn(async (url: string, init?: RequestInit) =>
      url.endsWith(icons) ? Response.json({ attestations: [] }) : fetchImpl(url, init),
    );
    await expect(verifyRenderer({ dir, fetchImpl: failing, verifier })).rejects.toThrow(
      /icons\.json: no valid provenance attestation/,
    );
  });
});

describe('bumpRenderer', () => {
  const withManifest = (change: Record<string, string>) => ({
    ...content,
    'renderer.manifest.json': JSON.stringify({ ...JSON.parse(content['renderer.manifest.json']), ...change }),
  });

  beforeEach(() => rmSync(join(dir, 'RENDERER')));

  it('pins an immutable release built from the tagged commit', async () => {
    const verifier = { verify: vi.fn() };
    const written = await bumpRenderer(TAG, { dir, fetchImpl: github(), verifier });
    expect(written).toEqual(pin);
    expect(parsePin(readFileSync(join(dir, 'RENDERER'), 'utf8'))).toEqual(pin);
    expect(readFileSync(join(dir, 'renderer.js'), 'utf8')).toBe(content['renderer.js']);
    expect(verifier.verify).toHaveBeenCalledTimes(REQUIRED_FILES.length);
  });

  it('writes the trusted repository and signer, whatever an old pin says', async () => {
    writeFileSync(join(dir, 'RENDERER'), formatPin(pin).replace(SIGNER, 'https://example.com/other'));
    await bumpRenderer(TAG, { dir, fetchImpl: github(), verifier: { verify: vi.fn() } });
    expect(parsePin(readFileSync(join(dir, 'RENDERER'), 'utf8')).signer).toBe(SIGNER);
  });

  it.each<[string, Release, RegExp]>([
    ['a release that is not immutable', { immutable: false }, /not immutable/],
    ['a release without a required file', { files: { ...content, 'icons.json': undefined! } }, /no icons\.json/],
    ['a file built from another commit', { builtFrom: 'b'.repeat(40) }, /built from commit b+, not a+/],
    ['a manifest of another version', { files: withManifest({ version: '9.9.9' }) }, /not the tag version/],
    [
      'a manifest whose sha256 is not renderer.js',
      { files: withManifest({ sha256: '0'.repeat(64) }) },
      /not the hash of renderer\.js/,
    ],
    [
      'a package.json of another version',
      { files: { ...content, 'package.json': JSON.stringify({ version: '1.2.2' }) } },
      /package\.json: version 1\.2\.2 is not the tag version 1\.2\.3/,
    ],
    ['a file built on a self-hosted runner', { runner: 'self-hosted' }, /self-hosted, not github-hosted/],
  ])('rejects %s and writes nothing', async (_, release, error) => {
    const files = Object.fromEntries(
      Object.entries(release.files ?? content).filter(([, value]) => value !== undefined),
    );
    await expect(
      bumpRenderer(TAG, { dir, fetchImpl: github({ ...release, files }), verifier: { verify: vi.fn() } }),
    ).rejects.toThrow(error);
    expect(existsSync(join(dir, 'RENDERER'))).toBe(false);
    expect(existsSync(join(dir, 'renderer.js'))).toBe(false);
  });

  it('rejects a tag that is not a Renderer release before any request', async () => {
    const fetchImpl = github();
    await expect(bumpRenderer('v1.0.0', { dir, fetchImpl, verifier: { verify: vi.fn() } })).rejects.toThrow(
      /release tag/,
    );
    expect(fetchImpl).not.toHaveBeenCalled();
  });
});
