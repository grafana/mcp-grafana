// The viz Renderer is not in Git: renderer/RENDERER pins a grafana/dataViz release, and scripts/renderer.mjs
// downloads it. Vitest runs this before it loads the test files, so their imports of ../renderer/* resolve.
import { fetchRenderer } from '../scripts/renderer.mjs';

export default async function setup() {
  await fetchRenderer({ log: (message: string) => console.error(message) });
}
