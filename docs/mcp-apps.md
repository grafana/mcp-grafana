# MCP Apps

MCP Apps render interactive views inside compatible MCP hosts. Each app is attached to a tool and appears whenever that tool is enabled:

```sh
mcp-grafana --enabled-tools=tempo,prometheus
```

Add other categories to the comma-separated list as needed. `--disable-query` or `--disable-tempo` removes the trace tool; `--disable-prometheus` removes the metrics view; `--disable-write` keeps both because they only read data.

## Tools

- `get_tempo_trace(trace_id, datasourceUid, focus_span_id?)` retrieves a Tempo trace and displays a virtualized waterfall, span filtering, attributes and exception details. The optional focus span selects an initial span without filtering the trace. The datasource must be Tempo.
- `query_prometheus(expr, datasourceUid, …)` displays the query result as a chart. The visualization is derived from the shape of the response and the metric's unit, never from a model-supplied argument:

  | Response | View |
  | --- | --- |
  | Range query | Time series |
  | Range query, cumulative histogram buckets (`le` labels) | Heatmap, de-cumulated per bucket |
  | Range query, 20 or more series | Heatmap |
  | Instant query, several values, bounded unit | Bullets on a shared scale |
  | Instant query, several values | Ranked bars |
  | Instant query, one value, bounded unit | Bullet |
  | Instant query, one value | Stat |

  A bounded unit is one that carries its own range, such as `percent` or `percentunit`. Nothing is scaled against an invented maximum, because Prometheus supplies no bounds. A table is always available, since it can represent any response shape. The viewer offers the alternatives that suit the same data in a picker, re-runs the query through the host on request, and can hand a selected time window back to the agent as a follow-up question. Units are inferred from the metric name (`_seconds`, `_bytes`, `_ratio`), since a PromQL response carries no field configuration.

Both tools preserve their existing text output and add viewer data when the response can be converted. Tempo’s LLM response format can change, so viewer conversion is best effort. Unsupported or partial responses retain their text output without an interactive view. Existing response-size limits still apply. Normalized viewer data is limited to 1 MiB; larger traces and wider query results retain their text output without an interactive view, rather than truncating data. Viewer data is optional structured content alongside the existing text output. Hosts without MCP Apps support can use the text output.

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

For selective UI resource registration, use `RegisterTraceAppResource` or `RegisterMetricsAppResource`. `GetTempoTraceTool` links to the exported `TraceViewerResourceURI`, and `QueryPrometheus` to `MetricsViewerResourceURI`.

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

One constraint is easy to miss when adding an app:

- **Attach host handlers before React renders.** A host may finish the handshake
  and deliver `tool-input` and `tool-result` before the first commit, and it does
  not replay them. Registering `app.ontoolresult` inside an effect makes
  rendering depend on that race. The metrics app keeps host state in
  `app/metricsHostState.ts`, created alongside the `App` and consumed with
  `useSyncExternalStore`.

`preview/localhost.html` runs an app against a real `AppBridge` host with the
iframe same-origin, so its console and errors are reachable. Hosts sandbox apps
and may refuse to expose a debugger, which makes an in-host failure otherwise
invisible; `npm run dev` serves this page.
