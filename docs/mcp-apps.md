# MCP Apps

MCP Apps render interactive views inside compatible MCP hosts. The trace viewer is attached to `get_tempo_trace` and is available whenever Tempo query tools are enabled:

```sh
mcp-grafana --enabled-tools=tempo
```

Add other categories to the comma-separated list as needed. `--disable-query` or `--disable-tempo` removes the trace tool; `--disable-write` keeps it because it only reads data.

## Tools

- `get_tempo_trace(trace_id, datasourceUid, focus_span_id?)` retrieves a Tempo trace and displays a virtualized waterfall, span filtering, attributes and exception details. The optional focus span selects an initial span without filtering the trace. The datasource must be Tempo.

The tool preserves its existing text output and adds viewer data when the response can be converted. Tempo’s LLM response format can change, so viewer conversion is best effort. Unsupported or partial responses retain their text output without an interactive view. Existing response-size limits still apply. Normalized viewer data is limited to 1 MiB; larger traces retain their text output without an interactive view, rather than truncating spans. Viewer data is optional structured content alongside the existing text output. Hosts without MCP Apps support can use the text output.

Tools use the configured Grafana connection and caller identity. Credentials
stay on the server. Apps are self-contained HTML with no external connection or
resource domains; trace sharing requests host-controlled clipboard permission.

## Embed in another MCP server

The Go module exports the same tools and UI used by the standalone server:

```go
import (
    mcpgrafana "github.com/grafana/mcp-grafana/v2"
    "github.com/grafana/mcp-grafana/v2/tools"
)

// s is an existing *mcp.Server. Its request context must provide the
// normal mcp-grafana configuration and Grafana client.
mcpgrafana.RegisterAppResources(s)
tools.AddTempoTools(s, enableQueryTools)
```

For selective UI resource registration, use `RegisterTraceAppResource`. The existing `GetTempoTraceTool` links to the exported `TraceViewerResourceURI`.

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
