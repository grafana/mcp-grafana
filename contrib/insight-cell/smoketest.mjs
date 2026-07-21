// End-to-end smoke test: spawn the stdio server, list tools, render each panel
// type, and read the ui:// resource. Verifies the MCP App wiring without a host.
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";

const transport = new StdioClientTransport({ command: "npx", args: ["tsx", "server.ts"], cwd: process.cwd() });
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

await client.close();
console.log("\nOK — all checks passed.");
