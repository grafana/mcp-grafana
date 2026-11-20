import { appPort, sandboxUrl } from './ports.mjs';
import { defineConfig } from 'vite';
import { Client } from '@modelcontextprotocol/client';
import { StdioClientTransport } from '@modelcontextprotocol/client/stdio';
import { fileURLToPath } from 'node:url';
import { execFile } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { promisify } from 'node:util';
import { createServer } from 'node:http';
import responseFixture from '@grafana/viz-contract/fixtures/prometheus-line/response.json' with { type: 'json' };

export default defineConfig({
  define: { __MCP_SANDBOX_URL__: JSON.stringify(sandboxUrl) },
  root: fileURLToPath(new URL('.', import.meta.url)),
  server: { host: '127.0.0.1', port: appPort, strictPort: true },
  plugins: [{
    name: 'mcp-resource-proxy',
    async configureServer(server) {
      const client = new Client({ name: 'browser-e2e', version: '1.0.0' }, {
        capabilities: {
          extensions: { 'io.modelcontextprotocol/ui': { mimeTypes: ['text/html;profile=mcp-app'] } },
        },
      });
      // Only Grafana HTTP is stubbed. The Go server still handles actual MCP tool calls.
      const requests: { queries?: { datasource?: { uid?: string } }[] }[] = [];
      const grafana = createServer(async (req, res) => {
        if (req.method !== 'POST' || req.url !== '/api/ds/query') {
          res.writeHead(404).end('Unexpected Grafana request');
          return;
        }
        let body = '';
        for await (const chunk of req) body += chunk;
        requests.push(JSON.parse(body));
        res.writeHead(responseFixture.status, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify(responseFixture.body));
      });
      await new Promise<void>(resolve => grafana.listen(0, '127.0.0.1', resolve));
      const address = grafana.address() as { port: number };
      const override = process.env.MCP_GRAFANA_BINARY;
      const buildDir = override ? undefined : await mkdtemp(join(tmpdir(), 'mcp-grafana-harness-'));
      const command = override ? resolve(override) : join(buildDir!, 'mcp-grafana');
      const cleanup = async () => {
        try {
          await client.close();
        } finally {
          await new Promise<void>((resolve, reject) => grafana.close(error => error ? reject(error) : resolve()));
          if (buildDir) await rm(buildDir, { recursive: true, force: true });
        }
      };
      try {
        if (buildDir) {
          server.config.logger.info('Building MCP server (using the Go build cache)…');
          await promisify(execFile)('go', ['build', '-o', command, './cmd/mcp-grafana'], {
            cwd: fileURLToPath(new URL('../../../', import.meta.url)),
          });
        }
        await client.connect(new StdioClientTransport({
          command,
          args: ['--enabled-tools', 'datasource,dsquery,prometheus,tempo,rendering'],
          // Drain server diagnostics through Vite; an unread pipe can block the Go process.
          stderr: 'inherit',
          env: { GRAFANA_URL: `http://127.0.0.1:${address.port}`, GRAFANA_SERVICE_ACCOUNT_TOKEN: '', GRAFANA_API_KEY: '' },
        }));
      } catch (error) {
        await cleanup();
        throw error;
      }
      server.httpServer?.on('close', () => void cleanup());
      server.middlewares.use(async (req, res, next) => {
        const url = new URL(req.url!, 'http://localhost');
        if (!url.pathname.startsWith('/mcp/')) return next();
        try {
          if (url.pathname === '/mcp/strict-app') {
            const resource = await client.readResource({ uri: 'ui://grafana/app.html' });
            const content = resource.contents[0];
            const sandbox = new URL(sandboxUrl);
            const ui = content._meta?.ui as { csp?: unknown } | undefined;
            if (ui?.csp) sandbox.searchParams.set('csp', JSON.stringify(ui.csp));
            const upstream = await fetch(sandbox);
            const csp = upstream.headers.get('content-security-policy');
            if (!upstream.ok || !csp) throw new Error('Upstream sandbox CSP unavailable');
            res.setHeader('Content-Security-Policy', csp);
            res.setHeader('Content-Type', 'text/html');
            if (!('text' in content)) throw new Error('App resource must contain HTML text');
            res.end(content.text);
            return;
          }
          if (url.pathname === '/mcp/call' && req.method === 'POST') {
            let body = '';
            for await (const chunk of req) body += chunk;
            const call = JSON.parse(body);
            // This development endpoint only exposes the fixture-backed visualization tool.
            if (call.name !== 'render_grafana_app' || call.arguments?.toolName !== 'query_datasource') {
              throw new Error('Only fixture query_datasource calls are supported');
            }
            const result = await client.callTool(call);
            const uid = call.arguments.arguments.datasourceUid;
            res.setHeader('Content-Type', 'application/json');
            res.end(JSON.stringify({ result, requests: requests.filter(request =>
              request.queries?.some(query => query.datasource?.uid === uid)) }));
            return;
          }
          const result = url.pathname === '/mcp/tools'
            ? await client.listTools()
            : await client.readResource({ uri: url.searchParams.get('uri')! });
          res.setHeader('Content-Type', 'application/json');
          res.end(JSON.stringify(result));
        } catch (error) {
          res.statusCode = 500;
          res.end(JSON.stringify({ error: String(error) }));
        }
      });
    },
  }],
});
