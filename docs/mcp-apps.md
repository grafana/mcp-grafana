# MCP Apps

MCP Apps render interactive views inside compatible MCP hosts. Metrics and traces share one explicit `render_grafana_app` tool in the `rendering` category:

```sh
mcp-grafana --enabled-tools=tempo,prometheus,rendering
```

Add other categories to the comma-separated list as needed. Add the opt-in `dsquery` category to display a query to any datasource:

```sh
mcp-grafana --enabled-tools=prometheus,rendering,dsquery
```

`--disable-query` removes source queries and the App tool. Disabling a source category removes it from the App tool; the App tool is omitted when no supported source remains. `--disable-rendering` leaves data queries available. `--disable-write` retains these read-only tools; it removes the `query_datasource` source (see below).

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

- `render_grafana_app` with `toolName: "query_datasource"` (opt-in `dsquery` category) displays a query to any datasource that Grafana queries on its backend. `arguments` is `datasourceUid`, `queries` (the queries of an `/api/ds/query` request: `refId` and the datasource fields, for example Prometheus `expr` and `legendFormat`), and optional `from` and `to` (default `now-1h` and `now`). `query_datasource` is not a tool of its own: the model reaches it only through `render_grafana_app`, so it adds no tool to `tools/list`. The server fills a missing `refId`, `maxDataPoints` (1000) and `intervalMs`, posts the request through the same `/api/ds/query` path as the other query tools, and returns the frames unchanged. It picks the panel type from the shape of the frames with the rule table of the Grafana ECharts panel renderer (`tools/app_datasource_pick.json`); the model does not choose it. The view draws the frames with that renderer, so series names from `legendFormat` and the datasource's field config are kept. Its Refresh and time picker re-run the query through `render_grafana_app` in the same view.

  The queries reach the datasource unfiltered, so raw SQL can write when the datasource credentials allow it. Like `query_sql`, the source is removed by `--disable-write` unless `--enable-query` or `--enable-write-tools=query_datasource` keeps it, and by `--disable-query`. While it is enabled, `render_grafana_app` is not annotated as read-only. It is also a bypass of `--loki-enforced-matchers`; the server warns about it at startup.

  The renderer (`ui/mcp-apps/vendor`, AGPL-3.0-only) adds about 1 MiB gzip. The server serves the App build that contains it only when `dsquery` is enabled; otherwise `ui://grafana/app.html` is the same as without it.

Source queries preserve their JSON text output and add viewer data when the response can be converted. The trace viewer is built from the trace frame Grafana’s Tempo datasource returns. Normalized viewer data is limited to 1 MiB. Source queries retain their text output for oversized or partial responses, subject to existing response-size limits.

The App tool returns viewer data in `structuredContent` and a short display confirmation in `content`. When viewer data is unavailable, it returns a tool error directing callers to the source query. Hosts that need text output can call the source query directly.

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

`RegisterGrafanaAppResource` registers only the shared `GrafanaAppResourceURI`. To display any datasource query, also pass `tools.QueryDatasourceSource` to `AddAppTool`, and `mcpgrafana.WithDatasourceView()` to `RegisterAppResources` or `RegisterGrafanaAppResource`; do not register the source as a tool. Pass only enabled read-only sources to `AddAppTool`; the standalone server applies its category and query gates before registration. The metrics view refreshes through `query_prometheus` in its existing App instance.

The module embeds the built HTML. Go consumers serve the bundled App without a Node toolchain and receive UI updates through the module version.

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
