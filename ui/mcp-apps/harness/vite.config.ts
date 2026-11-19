import { defineConfig } from 'vite';
import { Client } from '@modelcontextprotocol/client';
import { StdioClientTransport } from '@modelcontextprotocol/client/stdio';
import { fileURLToPath } from 'node:url';
import { execFile } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { promisify } from 'node:util';

export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  server: { host: '127.0.0.1', port: 5194, strictPort: true },
  plugins: [{
    name: 'mcp-resource-proxy',
    async configureServer(server) {
      const client = new Client({ name: 'browser-e2e', version: '1.0.0' }, {
        capabilities: {
          extensions: { 'io.modelcontextprotocol/ui': { mimeTypes: ['text/html;profile=mcp-app'] } },
        },
      });
      const override = process.env.MCP_GRAFANA_BINARY;
      const buildDir = override ? undefined : await mkdtemp(join(tmpdir(), 'mcp-grafana-harness-'));
      const command = override ? resolve(override) : join(buildDir!, 'mcp-grafana');
      const cleanup = async () => {
        try {
          await client.close();
        } finally {
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
          args: ['--enabled-tools', 'datasource,prometheus,tempo,rendering'],
          stderr: 'pipe',
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
