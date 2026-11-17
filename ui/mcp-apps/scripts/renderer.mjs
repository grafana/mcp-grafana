#!/usr/bin/env node
// Gets the viz Renderer of grafana/dataViz that renderer/RENDERER pins. See renderer/README.md.
//
//   node scripts/renderer.mjs                Download the pinned files that are missing or changed. Fails on a wrong hash.
//   node scripts/renderer.mjs --verify       Also verify the SLSA provenance attestation of each file (needs network).
//   node scripts/renderer.mjs --bump <tag>   Pin a release: download and verify its files, then write RENDERER.
//
// The default mode needs no gh CLI and no token, and no network when the files on disk match the pin.
// GITHUB_TOKEN, when set, is sent to the GitHub API (attestations, releases) to avoid the anonymous rate limit.
// RENDERER_LOCAL_DIR=<dir> copies an unreleased Renderer build (for example dataViz dist-renderer/) instead.
import { createHash } from 'node:crypto';
import { copyFile, readdir, readFile, rename, rm, writeFile } from 'node:fs/promises';
import http from 'node:http';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const rendererDir = fileURLToPath(new URL('../renderer/', import.meta.url));
export const PIN_FILE = 'RENDERER';

// The trust anchor. RENDERER repeats these two values so a reader sees them, but the script does not take them from
// RENDERER: a pin that names another repository or signer fails. To trust another one, change them here, in a file
// that CODEOWNERS gives to the viz Renderer owners.
export const REPOSITORY = 'grafana/dataViz';
export const SIGNER = `https://github.com/${REPOSITORY}/.github/workflows/release-please.yml@refs/heads/main`;

export const ISSUER = 'https://token.actions.githubusercontent.com';
export const PROVENANCE = 'https://slsa.dev/provenance/v1';
// Files the App and its tests read. A release without one of them cannot be pinned.
export const REQUIRED_FILES = ['renderer.js', 'renderer.manifest.json', 'renderer.d.ts', 'icons.json', 'package.json'];

const TAG = /^grafana-echarts-renderer\/v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$/;
// A file name from the pin or from a release: no directory part, so a write cannot leave renderer/.
const FILE_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
const HASH = /^[0-9a-f]{64}$/;
const COMMIT = /^[0-9a-f]{40}$/;
const SOURCE = `git+https://github.com/${REPOSITORY}@`;

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
  for (const key of ['repository', 'tag', 'commit', 'signer', 'version', 'contractVersion']) {
    if (!pin[key]) throw new Error(`${PIN_FILE}: no ${key} line`);
  }
  if (!COMMIT.test(pin.commit)) throw new Error(`${PIN_FILE}: bad commit ${pin.commit}`);
  if (pin.repository !== REPOSITORY || pin.signer !== SIGNER) {
    throw new Error(
      `${PIN_FILE}: repository ${pin.repository} and signer ${pin.signer} are not the trusted ${REPOSITORY} and ` +
        `${SIGNER}. Only scripts/renderer.mjs sets what is trusted.`,
    );
  }
  if (tagVersion(pin.tag) !== pin.version) {
    throw new Error(`${PIN_FILE}: version ${pin.version} is not the version of tag ${pin.tag}`);
  }
  for (const name of REQUIRED_FILES) {
    if (!pin.files[name]) throw new Error(`${PIN_FILE}: no sha256 line for ${name}`);
  }
  return pin;
}

export function formatPin(pin) {
  const lines = [
    `repository: ${pin.repository}`,
    `tag: ${pin.tag}`,
    `commit: ${pin.commit}`,
    `signer: ${pin.signer}`,
    `version: ${pin.version}`,
    `contractVersion: ${pin.contractVersion}`,
  ];
  for (const name of Object.keys(pin.files).sort()) lines.push(`sha256 ${name}: ${pin.files[name]}`);
  return `${lines.join('\n')}\n`;
}

export const downloadURL = (repository, tag, name) =>
  `https://github.com/${repository}/releases/download/${tag}/${name}`;

// Node fetch ignores HTTPS_PROXY unless NODE_USE_ENV_PROXY is set. Turn the proxy settings on, so the download works
// on a network that allows traffic only through a proxy.
let proxyFromEnv = false;
function useEnvProxy() {
  if (proxyFromEnv) return;
  proxyFromEnv = true;
  const env = process.env;
  const proxy = env.HTTPS_PROXY || env.https_proxy || env.HTTP_PROXY || env.http_proxy;
  if (proxy && typeof http.setGlobalProxyFromEnv === 'function') http.setGlobalProxyFromEnv();
}

async function request(url, { fetchImpl, token, json }) {
  if (fetchImpl === globalThis.fetch) useEnvProxy();
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
    throw new Error(`GET ${url}: ${error.message}${error.cause ? ` (${error.cause.message ?? error.cause})` : ''}`);
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

/** Copies a local Renderer build into renderer/. Nothing checks it: the pin tests fail until the next real fetch. */
async function copyLocal(dir, source, log) {
  const names = (await readdir(source)).filter((name) => FILE_NAME.test(name));
  for (const name of REQUIRED_FILES) {
    if (!names.includes(name)) throw new Error(`RENDERER_LOCAL_DIR ${source} has no ${name}`);
  }
  for (const name of names) await copyFile(join(source, name), join(dir, name));
  log(`Using the local Renderer build in ${source}. Its hashes are not checked, so the renderer-pin tests fail.`);
  return names;
}

/**
 * Downloads each pinned file that is missing or differs from its pin. Returns the names it downloaded.
 * With `localDir` (RENDERER_LOCAL_DIR by default), copies that build instead. `localDir: null` always uses the pin.
 */
export async function fetchRenderer({
  dir = rendererDir,
  fetchImpl = fetch,
  log = () => {},
  localDir = process.env.RENDERER_LOCAL_DIR || undefined,
} = {}) {
  const pin = parsePin(await readFile(join(dir, PIN_FILE), 'utf8'));
  if (localDir) return { pin, downloaded: [], local: await copyLocal(dir, resolve(localDir), log) };
  const downloaded = [];
  const replaced = [];
  for (const [name, want] of Object.entries(pin.files)) {
    const path = join(dir, name);
    const current = await readIfExists(path);
    if (current && sha256(current) === want) continue;
    const url = downloadURL(pin.repository, pin.tag, name);
    const data = await request(url, { fetchImpl });
    const got = sha256(data);
    if (got !== want) throw new Error(`${name}: sha256 ${got} is not the pin ${want} (${url})`);
    await writeAtomic(path, data);
    (current ? replaced : downloaded).push(name);
  }
  if (downloaded.length) log(`Downloaded ${downloaded.join(', ')} of ${pin.tag}.`);
  if (replaced.length) {
    log(
      `Replaced ${replaced.join(', ')} with the files of ${pin.tag}, because they did not match RENDERER. ` +
        'To use a local Renderer build, set RENDERER_LOCAL_DIR (see renderer/README.md).',
    );
  }
  return { pin, downloaded: [...downloaded, ...replaced] };
}

function provenanceStatement(bundle) {
  const envelope = bundle?.dsseEnvelope;
  if (envelope?.payloadType !== 'application/vnd.in-toto+json') return undefined;
  const statement = JSON.parse(Buffer.from(envelope.payload, 'base64').toString('utf8'));
  return statement.predicateType === PROVENANCE ? statement : undefined;
}

/** The commit of the trusted repository that a provenance statement says the file was built from. */
export function sourceCommit(statement) {
  const dependencies = statement?.predicate?.buildDefinition?.resolvedDependencies ?? [];
  return dependencies.find((dependency) => dependency?.uri?.startsWith(SOURCE))?.digest?.gitCommit;
}

/** The runner that a provenance statement says built the file: `github-hosted` or `self-hosted`. */
export function runnerEnvironment(statement) {
  return statement?.predicate?.buildDefinition?.internalParameters?.github?.runner_environment;
}

/**
 * Verifies that one file has a SLSA provenance attestation signed by the trusted SIGNER through public Sigstore, and
 * returns its statement. `verifier.verify` checks the signature, the certificate identity and the transparency log
 * entry. It does not compare a DSSE statement with the artifact, so the subject digest is compared here. The build
 * must also have run on a GitHub-hosted runner (as `gh attestation verify --deny-self-hosted-runners`), because a
 * self-hosted runner is outside what the signer identity proves.
 *
 * A file that is the same in several releases (icons.json, LICENSE) has one attestation per build. With `commit`, only
 * an attestation of the build of that commit passes, whatever order the API lists them in.
 */
export async function verifyFile(name, data, { verifier, fetchImpl = fetch, token, commit }) {
  const digest = sha256(data);
  const url = `https://api.github.com/repos/${REPOSITORY}/attestations/sha256:${digest}`;
  const { attestations = [] } = await request(url, { fetchImpl, token, json: true });
  const failures = [];
  for (const { bundle } of attestations) {
    const statement = provenanceStatement(bundle);
    if (!statement) continue;
    if (!(statement.subject ?? []).some((subject) => subject?.digest?.sha256 === digest)) {
      failures.push('subject digest is not the file hash');
      continue;
    }
    const runner = runnerEnvironment(statement);
    if (runner !== 'github-hosted') {
      failures.push(`built on runner environment ${runner ?? '(none)'}, not github-hosted`);
      continue;
    }
    if (commit && sourceCommit(statement) !== commit) {
      failures.push(`built from commit ${sourceCommit(statement)}, not ${commit}`);
      continue;
    }
    try {
      verifier.verify(bundle);
      return statement;
    } catch (error) {
      failures.push(error.message);
    }
  }
  const reason = failures.length ? failures.join('; ') : `no ${PROVENANCE} attestation`;
  throw new Error(`${name}: no valid provenance attestation from ${SIGNER}: ${reason}`);
}

async function createVerifier() {
  const sigstore = await import('sigstore');
  return sigstore.createVerifier({ certificateIssuer: ISSUER, certificateIdentityURI: SIGNER });
}

/** Fetches the pinned files, then verifies the attestation of each one. */
export async function verifyRenderer({ dir = rendererDir, fetchImpl = fetch, token, verifier, log = () => {} } = {}) {
  // The attestations are of the released files, so a local build cannot be verified. Use the pin, and say so.
  if (process.env.RENDERER_LOCAL_DIR) log('Ignoring RENDERER_LOCAL_DIR: --verify checks the pinned release.');
  const { pin } = await fetchRenderer({ dir, fetchImpl, log, localDir: null });
  verifier ??= await createVerifier();
  for (const name of Object.keys(pin.files)) {
    await verifyFile(name, await readFile(join(dir, name)), { verifier, fetchImpl, token, commit: pin.commit });
  }
  log(`Verified the provenance of ${Object.keys(pin.files).length} files of ${pin.tag}.`);
  return pin;
}

/** The commit that a tag names. An annotated tag points to a tag object, which points to the commit. */
async function tagCommit(tag, options) {
  let { object } = await request(`https://api.github.com/repos/${REPOSITORY}/git/ref/tags/${tag}`, {
    ...options,
    json: true,
  });
  if (object.type === 'tag') {
    ({ object } = await request(object.url, { ...options, json: true }));
  }
  if (object.type !== 'commit') throw new Error(`tag ${tag} does not name a commit`);
  return object.sha;
}

/**
 * Pins a release. The release must be immutable, and each file must have a valid provenance attestation from the
 * build of the tagged commit. Then it writes the files and RENDERER.
 */
export async function bumpRenderer(tag, { dir = rendererDir, fetchImpl = fetch, token, verifier, log = () => {} } = {}) {
  const version = tagVersion(tag);
  const options = { fetchImpl, token };
  const release = await request(`https://api.github.com/repos/${REPOSITORY}/releases/tags/${tag}`, {
    ...options,
    json: true,
  });
  if (release.immutable !== true) {
    throw new Error(`release ${tag} is not immutable, so its files can still change after the pin`);
  }
  const names = release.assets.map((asset) => asset.name);
  for (const name of names) {
    if (!FILE_NAME.test(name)) throw new Error(`release ${tag}: bad asset name ${JSON.stringify(name)}`);
  }
  for (const name of REQUIRED_FILES) {
    if (!names.includes(name)) throw new Error(`release ${tag}: no ${name}`);
  }
  const commit = await tagCommit(tag, options);

  verifier ??= await createVerifier();
  const files = {};
  const data = {};
  for (const name of names) {
    data[name] = await request(downloadURL(REPOSITORY, tag, name), { fetchImpl });
    files[name] = sha256(data[name]);
    await verifyFile(name, data[name], { verifier, ...options, commit });
  }

  const manifest = JSON.parse(data['renderer.manifest.json'].toString('utf8'));
  if (manifest.version !== version) {
    throw new Error(`renderer.manifest.json: version ${manifest.version} is not the tag version ${version}`);
  }
  if (manifest.sha256 !== files['renderer.js']) {
    throw new Error('renderer.manifest.json: sha256 is not the hash of renderer.js');
  }
  if (!manifest.contractVersion) throw new Error('renderer.manifest.json: no contractVersion');
  // The App reads the peers of the Renderer from package.json (renderer.9), so it must be of the same release.
  const packageVersion = JSON.parse(data['package.json'].toString('utf8')).version;
  if (packageVersion !== version) {
    throw new Error(`package.json: version ${packageVersion} is not the tag version ${version}`);
  }

  for (const name of names) await writeAtomic(join(dir, name), data[name]);
  const pin = {
    repository: REPOSITORY,
    tag,
    commit,
    signer: SIGNER,
    version,
    contractVersion: manifest.contractVersion,
    files,
  };
  await writeFile(join(dir, PIN_FILE), formatPin(pin));
  log(`Pinned ${tag} (contract ${manifest.contractVersion}, ${names.length} files, built from ${commit}).`);
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
