import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

// Bundles mcp-app.html + its TS/CSS into a single self-contained HTML file
// (dist/mcp-app.html). Single-file output means no external asset requests, so
// the MCP Apps deny-by-default CSP needs no extra configuration.
export default defineConfig({
  plugins: [viteSingleFile()],
  build: {
    outDir: "dist",
    emptyOutDir: false, // mcp-app.html and preview.html build separately into dist
    rollupOptions: {
      input: process.env.INPUT,
    },
  },
});
