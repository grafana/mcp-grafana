# MCP Apps

MCP Apps render interactive views inside compatible MCP hosts. The `mcp-apps`
category is opt-in:

```sh
mcp-grafana --enabled-tools=mcp-apps
```

Add other categories to the comma-separated list as needed. `--disable-query`
or `--disable-mcp-apps` removes these tools; `--disable-write` keeps them because
they only read data.

## Tools

- `render_trace(trace_id, datasource_uid, focus_span_id?)` displays a Tempo trace
  with a virtualized waterfall, span filtering, attributes and exception details.
  The datasource must be Tempo. Oversized responses fail explicitly instead of
  silently dropping spans.
Tools use the configured Grafana connection and caller identity. Credentials
stay on the server. Apps are self-contained HTML with no external connection or
resource domains; trace sharing requests host-controlled clipboard permission.

## Embed in another MCP server

The Go module exports the same tools and UI used by the standalone server:

```go
import (
    mcpgrafana "github.com/grafana/mcp-grafana"
    "github.com/grafana/mcp-grafana/tools"
)

// s is an existing *server.MCPServer. Its request context must provide the
// normal mcp-grafana configuration and Grafana client.
mcpgrafana.RegisterAppResources(s)
tools.AddMCPAppTools(s, enableQueryTools)
```

For selective registration, use `tools.AddTraceAppTools` and
`RegisterTraceAppResource`. Its resource URI is exported as
`TraceViewerResourceURI`.

The module embeds the built HTML, so Go consumers need no Node toolchain to serve
these apps. Do not copy the bundles into the embedding server: update the Go
module version to pick up app changes.

## Build and reuse the shell

`ui/mcp-apps` contains the React shell, app entry points, previews and tests. Its
package exports the shell and app components for other app authors. All build
dependencies are available from public npm.

```sh
cd ui/mcp-apps
npm ci
npm test
npm run build
```

Run `make build-ui` from the repository root to rebuild all embedded apps. Commit
the resulting HTML alongside source changes; CI checks bundle drift. The shell
supports explicit light/dark modes, composable sections, host navigation,
loading/error feedback and accessible recovery actions.
