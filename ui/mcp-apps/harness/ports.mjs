// Keep preview, sandbox, and browser test URLs on the same ports.
function port(name, fallback) {
  const value = process.env[name] || String(fallback);
  if (!/^\d+$/.test(value) || Number(value) < 1 || Number(value) > 65535) {
    throw new Error(`${name} must be an integer between 1 and 65535`);
  }
  return Number(value);
}
export const appPort = port('MCP_APPS_HOST_PORT', 5194);
export const basicHostPort = port('MCP_BASIC_HOST_PORT', 5196);
export const sandboxPort = port('MCP_SANDBOX_HOST_PORT', 5197);
if (new Set([appPort, basicHostPort, sandboxPort]).size !== 3) {
  throw new Error('MCP Apps harness ports must be distinct');
}
export const appUrl = `http://127.0.0.1:${appPort}`;
export const sandboxUrl = `http://localhost:${sandboxPort}/sandbox.html`;
