import { defineConfig } from 'vite';
import { Client } from '@modelcontextprotocol/client';
import { StdioClientTransport } from '@modelcontextprotocol/client/stdio';
import { fileURLToPath } from 'node:url';

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
      await client.connect(new StdioClientTransport({
        command: fileURLToPath(new URL('../../../dist/mcp-grafana', import.meta.url)),
        args: ['--enabled-tools', 'datasource,prometheus,tempo,rendering'],
        stderr: 'pipe',
      }));
      server.httpServer?.on('close', () => void client.close());
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
