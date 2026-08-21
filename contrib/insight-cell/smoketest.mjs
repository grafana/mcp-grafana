// End-to-end smoke test: spawn the stdio server, list tools, render each panel
// type, and read the ui:// resource. Verifies the MCP App wiring without a host.
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import fs from "node:fs";

const smokeStore = ".smoke-cells"; // keep share roundtrips out of the real store
const transport = new StdioClientTransport({ command: "npx", args: ["tsx", "server.ts"], cwd: process.cwd(), env: { ...process.env, INSIGHT_CELL_STORE: smokeStore } });
const client = new Client({ name: "smoketest", version: "0.0.0" });
await client.connect(transport);

const tools = await client.listTools();
console.log("TOOL:", tools.tools[0].name, "| ui meta:", JSON.stringify(tools.tools[0]._meta));

for (const panel of ["timeseries", "stat", "bar", "table", "logs", "trace"]) {
  const result = await client.callTool({ name: "grafana_render", arguments: { panel } });
  const cell = result.structuredContent;
  const body = cell.frames ? `${cell.frames[0].fields.length} fields` : cell.logs ? `${cell.logs.length} logs` : cell.trace ? `${cell.trace.spans.length} spans` : "—";
  const v0 = result._meta?.["grafana.insightCell/v0"];
  console.log(`  ${panel.padEnd(11)} dataMode=${cell.meta.dataMode} | ${body} | v0=${!!v0} | actions=${cell.actions.length}`);
}

const res = await client.readResource({ uri: "ui://grafana-insight-cell/render-surface.html" });
console.log("RESOURCE:", res.contents[0].mimeType, res.contents[0].text.length, "bytes | has root:", res.contents[0].text.includes('id="root"'));

// Share roundtrip: render → share_cell (persist + link) → open_shared_cell (reload from the link).
const rendered = await client.callTool({ name: "grafana_render", arguments: { panel: "stat" } });
const shared = await client.callTool({ name: "share_cell", arguments: { cell: rendered.structuredContent } });
const link = shared.content[0].text.match(/Shared: (\S+)/)?.[1];
if (!link) throw new Error(`share_cell returned no link: ${shared.content[0].text}`);
const opened = await client.callTool({ name: "open_shared_cell", arguments: { link } });
const oc = opened.structuredContent;
if (!oc?.renderHint || oc.renderHint.title !== rendered.structuredContent.renderHint.title) {
  throw new Error("open_shared_cell did not return the shared cell");
}
console.log(`SHARE: ${link} | reopened '${oc.renderHint.title}' | shared by ${oc.meta.shared?.by ?? "?"} at ${oc.meta.shared?.at}`);
const missing = await client.callTool({ name: "open_shared_cell", arguments: { link: "deadbeef00" } });
console.log("SHARE (unknown id):", missing.content[0].text.split(".")[0]);

await client.close();
fs.rmSync(smokeStore, { recursive: true, force: true });
console.log("\nOK — all checks passed.");
