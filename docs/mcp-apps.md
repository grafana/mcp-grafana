# MCP Apps

MCP Apps render interactive views inside compatible MCP hosts. Metrics and traces share one explicit `render_grafana_app` tool in the `rendering` category:

```sh
mcp-grafana --enabled-tools=tempo,prometheus,rendering
```

Add other categories to the comma-separated list as needed. `--disable-query` removes source queries and the App tool. Disabling a source category removes it from the App tool; the App tool is omitted when no supported source remains. `--disable-rendering` leaves data queries available. `--disable-write` retains these read-only tools.

## Tools

- `get_tempo_trace(trace_id, datasourceUid, focus_span_id?, start?, end?)` returns trace text and structured data for analysis. The optional focus span selects an initial span without filtering the trace. The datasource must be Tempo.
- `query_prometheus(expr, datasourceUid, …)` returns data for analysis without opening a view.
- `render_grafana_app(toolName, arguments)` executes an enabled `query_prometheus` or `get_tempo_trace` call through its existing handler and displays its result. `arguments` reuses the selected tool's input schema. Set a dynamic `orgId` on the outer call. Trace results display a waterfall with span filtering, attributes and exception details. Metrics select a visualization from the response shape and unit:

  | Response | View |
  | --- | --- |
  | Range query | Time series |
  | Range query, cumulative histogram buckets (`le` labels) | Heatmap, de-cumulated per bucket |
  | Range query, 20 or more series | Heatmap |
  | Instant query, several values, bounded unit | Bullets on a shared scale |
  | Instant query, several values | Ranked bars |
  | Instant query, one value, bounded unit | Bullet |
  | Instant query, one value | Stat |

  A bounded unit is one that carries its own range, such as `percent` or `percentunit`. Nothing is scaled against an invented maximum, because Prometheus supplies no bounds. A table is always available, since it can represent any response shape. The viewer offers the alternatives that suit the same data in a picker, re-runs the query through the host on request, and can hand a selected time window back to the agent as a follow-up question. Units are inferred from the metric name (`_seconds`, `_bytes`, `_ratio`), since a PromQL response carries no field configuration. Functions such as `rate()` and `sum()` drop the name, so their results show plain numbers rather than a guessed unit, and `_count` and `_bucket` values show as counts. Views that cannot fit every series, such as the bar chart and heatmap, say how many they leave out.

Source queries preserve their JSON text output and add viewer data when the response can be converted. The trace viewer is built from the trace frame Grafana’s Tempo datasource returns. Normalized viewer data is limited to 1 MiB. Source queries retain their text output for oversized or partial responses, subject to existing response-size limits.

The App tool returns viewer data in `structuredContent` and a short display confirmation in `content`. When viewer data is unavailable, it returns a tool error directing callers to the source query. Hosts that need text output can call the source query directly. A client whose capabilities do not list the MCP Apps extension (`io.modelcontextprotocol/ui`) gets a tool error before the query runs, so viewer data never reaches the model as a tool result. A request with no client capabilities, such as a stateless HTTP request of an older protocol, keeps the view.

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
tools.AddTempoTools(s, true)
tools.AddPrometheusTools(s, true)
tools.AddAppTool(s, tools.QueryPrometheus, tools.GetTempoTraceTool)
```

`RegisterGrafanaAppResource` registers the shared `GrafanaAppResourceURI` and the viz Renderer script at `VizRendererResourceURI`. The Renderer URI holds a hash of the script, so it changes with each build; read it from the variable and do not hard-code it. Hosts list the Renderer in `resources/list`, but it is for the App, not the chat. Pass only enabled sources to `AddAppTool`; the standalone server applies its category, query and write gates before registration. The App tool is read-only only when every source is read-only, so a source such as `query_datasource` makes it destructive. The metrics view refreshes through `query_prometheus` in its existing App instance.

The module embeds the built HTML and the Renderer script. Go consumers serve the bundled App without a Node toolchain and receive UI updates through the module version.

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

Run `mise run build-ui` from the repository root to rebuild all embedded apps. Commit
the resulting HTML alongside source changes; CI checks bundle drift. The shell
supports explicit light/dark modes, composable sections, host navigation,
loading/error feedback and accessible recovery actions.

One constraint is easy to miss when adding an app:

- **Attach host handlers before React renders.** A host may finish the handshake
  and deliver `tool-input` and `tool-result` before the first commit, and it does
  not replay them. Registering `app.ontoolresult` inside an effect makes
  rendering depend on that race. The shared App keeps host state in
  `app/appHostState.ts`, created alongside the `App` and consumed with
  `useSyncExternalStore`.

`preview/localhost.html` runs an app against a real `AppBridge` host with the
iframe same-origin, so its console and errors are reachable. Hosts sandbox apps
and may refuse to expose a debugger, which makes an in-host failure otherwise
invisible; `npm run dev` serves this page.
