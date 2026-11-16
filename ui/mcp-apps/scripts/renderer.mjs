#!/usr/bin/env node
// Gets the viz Renderer of grafana/dataViz that renderer/RENDERER pins. See renderer/README.md.
//
//   node scripts/renderer.mjs                Download the pinned files that are missing or changed. Fails on a wrong hash.
//   node scripts/renderer.mjs --verify       Also verify the SLSA provenance attestation of each file (needs network).
//   node scripts/renderer.mjs --bump <tag>   Pin a release: download and verify its files, then write RENDERER.
//
// The default mode needs no gh CLI and no token, and no network when the files on disk match the pin.
// GITHUB_TOKEN, when set, is sent to the GitHub API (attestations, release assets) to avoid the anonymous rate limit.
import { createHash } from 'node:crypto';
import { readFile, rename, rm, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const rendererDir = fileURLToPath(new URL('../renderer/', import.meta.url));
export const PIN_FILE = 'RENDERER';
export const REPOSITORY = 'grafana/dataViz';
export const ISSUER = 'https://token.actions.githubusercontent.com';
export const SIGNER = `https://github.com/${REPOSITORY}/.github/workflows/release-please.yml@refs/heads/main`;
export const PROVENANCE = 'https://slsa.dev/provenance/v1';
// Files the App and its tests read. A release without one of them cannot be pinned.
export const REQUIRED_FILES = ['renderer.js', 'renderer.manifest.json', 'renderer.d.ts', 'icons.json', 'package.json'];

const TAG = /^grafana-echarts-renderer\/v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$/;
// A file name from the pin or from a release: no directory part, so a write cannot leave renderer/.
const FILE_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
const HASH = /^[0-9a-f]{64}$/;

export const sha256 = (data) => createHash('sha256').update(data).digest('hex');

/** The version of a release tag, for example 0.1.0 for grafana-echarts-renderer/v0.1.0. */
export function tagVersion(tag) {
  const match = TAG.exec(tag);
  if (!match) throw new Error(`not a Renderer release tag: ${tag} (expected grafana-echarts-renderer/v<semver>)`);
  return match[1];
}

export function parsePin(text) {
  const pin = { files: {} };
  for (const line of text.split('\n')) {
    const at = line.indexOf(': ');
    if (at < 0) continue;
    const key = line.slice(0, at);
    const value = line.slice(at + 2).trim();
    if (key.startsWith('sha256 ')) {
      const name = key.slice('sha256 '.length);
      if (!FILE_NAME.test(name)) throw new Error(`${PIN_FILE}: bad file name ${JSON.stringify(name)}`);
      if (!HASH.test(value)) throw new Error(`${PIN_FILE}: bad sha256 for ${name}`);
      pin.files[name] = value;
    } else {
      pin[key] = value;
    }
  }
  for (const key of ['repository', 'tag', 'signer']) {
    if (!pin[key]) throw new Error(`${PIN_FILE}: no ${key} line`);
  }
  tagVersion(pin.tag);
  for (const name of REQUIRED_FILES) {
    if (!pin.files[name]) throw new Error(`${PIN_FILE}: no sha256 line for ${name}`);
  }
  return pin;
}

export function formatPin(pin) {
  const lines = [`repository: ${pin.repository}`, `tag: ${pin.tag}`, `signer: ${pin.signer}`];
  for (const name of Object.keys(pin.files).sort()) lines.push(`sha256 ${name}: ${pin.files[name]}`);
  return `${lines.join('\n')}\n`;
}

export const downloadURL = (repository, tag, name) =>
  `https://github.com/${repository}/releases/download/${tag}/${name}`;

async function request(url, { fetchImpl, token, json }) {
  const headers = {};
  if (url.startsWith('https://api.github.com/')) {
    headers.Accept = 'application/vnd.github+json';
    headers['X-GitHub-Api-Version'] = '2022-11-28';
    if (token) headers.Authorization = `Bearer ${token}`;
  }
  let response;
  try {
    response = await fetchImpl(url, { headers, redirect: 'follow' });
  } catch (error) {
    throw new Error(`GET ${url}: ${error.message}`);
  }
  if (!response.ok) throw new Error(`GET ${url}: HTTP ${response.status}`);
  return json ? response.json() : Buffer.from(await response.arrayBuffer());
}

async function readIfExists(path) {
  try {
    return await readFile(path);
  } catch (error) {
    if (error.code === 'ENOENT') return undefined;
    throw error;
  }
}

/** Writes through a temporary file, so a failed write leaves no partial file under the final name. */
async function writeAtomic(path, data) {
  const temporary = `${path}.download`;
  try {
    await writeFile(temporary, data);
    await rename(temporary, path);
  } finally {
    await rm(temporary, { force: true });
  }
}

/** Downloads each pinned file that is missing or differs from its pin. Returns the names it downloaded. */
export async function fetchRenderer({ dir = rendererDir, fetchImpl = fetch, log = () => {} } = {}) {
  const pin = parsePin(await readFile(join(dir, PIN_FILE), 'utf8'));
  const downloaded = [];
  for (const [name, want] of Object.entries(pin.files)) {
    const path = join(dir, name);
    const current = await readIfExists(path);
    if (current && sha256(current) === want) continue;
    const url = downloadURL(pin.repository, pin.tag, name);
    const data = await request(url, { fetchImpl });
    const got = sha256(data);
    if (got !== want) throw new Error(`${name}: sha256 ${got} is not the pin ${want} (${url})`);
    await writeAtomic(path, data);
    downloaded.push(name);
  }
  if (downloaded.length) log(`Downloaded ${downloaded.join(', ')} of ${pin.tag}.`);
  return { pin, downloaded };
}

function provenanceSubjects(bundle) {
  const envelope = bundle?.dsseEnvelope;
  if (envelope?.payloadType !== 'application/vnd.in-toto+json') return undefined;
  const statement = JSON.parse(Buffer.from(envelope.payload, 'base64').toString('utf8'));
  if (statement.predicateType !== PROVENANCE) return undefined;
  return (statement.subject ?? []).map((subject) => subject?.digest?.sha256);
}

/**
 * Verifies that one file has a SLSA provenance attestation signed by `signer` through public Sigstore.
 * `verifier.verify` checks the signature, the certificate identity and the transparency log entry. It does not compare
 * a DSSE statement with the artifact, so the subject digest is compared here.
 */
export async function verifyFile(name, data, { repository, signer, verifier, fetchImpl = fetch, token }) {
  const digest = sha256(data);
  const url = `https://api.github.com/repos/${repository}/attestations/sha256:${digest}`;
  const { attestations = [] } = await request(url, { fetchImpl, token, json: true });
  const failures = [];
  for (const { bundle } of attestations) {
    const subjects = provenanceSubjects(bundle);
    if (!subjects) continue;
    if (!subjects.includes(digest)) {
      failures.push('subject digest is not the file hash');
      continue;
    }
    try {
      verifier.verify(bundle);
      return;
    } catch (error) {
      failures.push(error.message);
    }
  }
  const reason = failures.length ? failures.join('; ') : `no ${PROVENANCE} attestation`;
  throw new Error(`${name}: no valid provenance attestation from ${signer}: ${reason}`);
}

async function createVerifier(signer) {
  const sigstore = await import('sigstore');
  return sigstore.createVerifier({ certificateIssuer: ISSUER, certificateIdentityURI: signer });
}

/** Fetches the pinned files, then verifies the attestation of each one. */
export async function verifyRenderer({ dir = rendererDir, fetchImpl = fetch, token, verifier, log = () => {} } = {}) {
  const { pin } = await fetchRenderer({ dir, fetchImpl, log });
  verifier ??= await createVerifier(pin.signer);
  for (const name of Object.keys(pin.files)) {
    await verifyFile(name, await readFile(join(dir, name)), { ...pin, verifier, fetchImpl, token });
  }
  log(`Verified the provenance of ${Object.keys(pin.files).length} files of ${pin.tag}.`);
  return pin;
}

function checkManifest(tag, files, data) {
  const manifest = JSON.parse(data['renderer.manifest.json'].toString('utf8'));
  if (manifest.version !== tagVersion(tag)) {
    throw new Error(`renderer.manifest.json: version ${manifest.version} is not the tag version ${tagVersion(tag)}`);
  }
  if (manifest.sha256 !== files['renderer.js']) {
    throw new Error('renderer.manifest.json: sha256 is not the hash of renderer.js');
  }
}

/** Pins a release: downloads and verifies all its files, then writes them and RENDERER. */
export async function bumpRenderer(tag, { dir = rendererDir, fetchImpl = fetch, token, verifier, log = () => {} } = {}) {
  tagVersion(tag);
  const previous = await readIfExists(join(dir, PIN_FILE));
  // Keep the repository and signer of a valid pin. A missing or old-format pin starts from the defaults.
  let old = {};
  try {
    if (previous) old = parsePin(previous.toString('utf8'));
  } catch {
    old = {};
  }
  const repository = old.repository ?? REPOSITORY;
  const signer = old.signer ?? SIGNER;

  const release = await request(`https://api.github.com/repos/${repository}/releases/tags/${tag}`, {
    fetchImpl,
    token,
    json: true,
  });
  const names = release.assets.map((asset) => asset.name);
  for (const name of names) {
    if (!FILE_NAME.test(name)) throw new Error(`release ${tag}: bad asset name ${JSON.stringify(name)}`);
  }
  for (const name of REQUIRED_FILES) {
    if (!names.includes(name)) throw new Error(`release ${tag}: no ${name}`);
  }

  verifier ??= await createVerifier(signer);
  const files = {};
  const data = {};
  for (const name of names) {
    data[name] = await request(downloadURL(repository, tag, name), { fetchImpl });
    files[name] = sha256(data[name]);
    await verifyFile(name, data[name], { repository, signer, verifier, fetchImpl, token });
  }
  checkManifest(tag, files, data);

  for (const name of names) await writeAtomic(join(dir, name), data[name]);
  const pin = { repository, tag, signer, files };
  await writeFile(join(dir, PIN_FILE), formatPin(pin));
  log(`Pinned ${tag} (${names.length} files, provenance verified).`);
  return pin;
}

async function main(args) {
  const log = (message) => console.error(message);
  const token = process.env.GITHUB_TOKEN || undefined;
  if (args.length === 0) return fetchRenderer({ log });
  if (args.length === 1 && args[0] === '--verify') return verifyRenderer({ token, log });
  if (args.length === 2 && args[0] === '--bump') {
    await bumpRenderer(args[1], { token, log });
    log('Next: npm test, then mise run build-ui in the repository root, then commit renderer/RENDERER and dist/.');
    return;
  }
  throw new Error('usage: node scripts/renderer.mjs [--verify | --bump <tag>]');
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(`renderer: ${error.message}`);
    process.exit(1);
  });
}
