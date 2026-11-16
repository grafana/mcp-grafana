# MCP Apps

MCP Apps render interactive views inside compatible MCP hosts. Metrics, traces and logs share one explicit `render_grafana_app` tool in the `rendering` category:

```sh
mcp-grafana --enabled-tools=tempo,prometheus,loki,rendering
```

Add other categories to the comma-separated list as needed. `--disable-query` removes source queries and the App tool. Disabling a source category removes it from the App tool; the App tool is omitted when no supported source remains. `--disable-rendering` leaves data queries available. `--disable-write` retains these read-only tools.

## Tools

- `get_tempo_trace(trace_id, datasourceUid, focus_span_id?, start?, end?)` returns trace text and structured data for analysis. The optional focus span selects an initial span without filtering the trace. The datasource must be Tempo.
- `query_loki_logs(logql, datasourceUid, …)` returns logs or Loki metric samples for analysis without opening a view.
- `query_prometheus(expr, datasourceUid, …)` returns data for analysis without opening a view.
- `render_grafana_app(toolName, arguments)` executes an enabled `query_prometheus`, `get_tempo_trace` or `query_loki_logs` call through its existing handler and displays its result. `arguments` reuses the selected tool's input schema. Set a dynamic `orgId` on the outer call. Trace results display a waterfall with span filtering, attributes and exception details. Metrics select a visualization from the response shape and unit:

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
tools.Resolve(tools.Policy{Categories: []string{"prometheus", "tempo"}}).Register(s)
tools.AddAppTool(s, tools.QueryPrometheus, tools.GetTempoTraceTool)
```

`RegisterGrafanaAppResource` registers only the shared `GrafanaAppResourceURI`. When `query_datasource` is a source, `AddAppTool` and `Resolve(...).Register` also register the viz Renderer script that the App reads to draw its results. The script URI holds a hash of the script, so it changes with each build. The resource has the `user` audience, because it is for the App, not the model. Pass only enabled sources to `AddAppTool`; the standalone server applies its category, query and write gates before registration. The App tool is read-only only when every source is read-only, so a source such as `query_datasource` makes it destructive. The metrics view refreshes through `query_prometheus` in its existing App instance.

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

## Logs

Display log lines through the same renderer:

```json
{
  "toolName": "query_loki_logs",
  "arguments": {
    "datasourceUid": "my-loki",
    "logql": "{service_name=\"checkout\"} |= \"error\"",
    "startRfc3339": "now-15m",
    "endRfc3339": "now",
    "limit": 100
  }
}
```

The log view shows timestamps at their original precision, single-line messages,
and colored severity levels. Expanding a row reveals its complete multiline message
in place, followed by Indexed labels,
Parsed fields and Structured metadata in collapsible sections.
Full and compact source output produce the same chronological view;
`direction` controls oldest or newest first. The query time range and returned
line count appear above the results, and a notice identifies truncated results.
Loki metric queries explain that a log query is required for this view.

Refresh and Retry use the host's authenticated MCP session, preserving query
arguments and any outer `orgId`. Open in Grafana uses the configured public URL
when a range query has known bounds and the link can resolve in the query organization. The existing Loki query limits,
permissions and cost guardrails apply. Source-query JSON remains unchanged;
viewer data is supplied separately within the shared 1 MiB budget.

Enable `loki,rendering` to use logs without the other app sources. Embedding
servers can pass `tools.QueryLokiLogs` to `tools.AddAppTool` after registering
Loki tools and the shared app resource. Pattern discovery and log-volume charts
are outside this release.

For a synthetic AppBridge preview, build `ui/mcp-apps`, run
`npx vite --config localhost.vite.config.ts --host 127.0.0.1 --port 5188`, and open
`http://127.0.0.1:5188/localhost.html?fixture=logs&mode=dark`.
The preview does not query Grafana; `&drive=refresh` exercises the refresh round trip.
