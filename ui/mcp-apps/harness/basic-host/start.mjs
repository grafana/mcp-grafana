import { createHash } from 'node:crypto';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { build } from 'vite';
import { viteSingleFile } from 'vite-plugin-singlefile';

const manifest = JSON.parse(await readFile(new URL('./upstream.json', import.meta.url), 'utf8'));
const root = fileURLToPath(new URL('./upstream-cache/', import.meta.url));
for (const [source, hash] of Object.entries(manifest.files)) {
  const relative = source.replace('examples/basic-host/', '');
  const target = new URL(`./upstream-cache/${relative}`, import.meta.url);
  let data;
  try { data = await readFile(target); } catch {}
  if (!data || createHash('sha256').update(data).digest('hex') !== hash) {
    const response = await fetch(`https://raw.githubusercontent.com/${manifest.repository}/${manifest.revision}/${source}`);
    if (!response.ok) throw new Error(`Upstream download failed: ${source}: ${response.status}`);
    data = Buffer.from(await response.arrayBuffer());
  }
  if (createHash('sha256').update(data).digest('hex') !== hash) throw new Error(`Upstream hash mismatch: ${source}`);
  await mkdir(new URL('.', target), { recursive: true });
  await writeFile(target, data);
}
// Build upstream's sandbox entry unchanged. Only bundler configuration is local.
await build({ configFile: false, root, plugins: [viteSingleFile()],
  build: { outDir: 'dist', rollupOptions: { input: `${root}/sandbox.html` } } });
const child = spawn(process.execPath, [`${root}/serve.ts`], {
  stdio: 'inherit', env: { ...process.env, HOST_PORT: '5196', SANDBOX_PORT: '5197' },
});
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => child.kill(signal));
child.on('exit', code => process.exit(code ?? 1));
