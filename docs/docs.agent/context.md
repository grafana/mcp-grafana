# context.md

Follow and keep this documentation context and plan up to date.

This repository **github.com/grafana/mcp-grafana** is the main context source.

If you have additional project or repository context sources, list them here:

- [Model Context Protocol](https://modelcontextprotocol.io/) specification
- [Grafana API documentation](https://grafana.com/docs/grafana/latest/developers/http_api/)

Output docs to `docs/sources/`.

Order sections as they appear below: Clients, then Set up, then Configure.

Add articles as h3 subsections with a list of:

- Relevant source files for context
- Things to include and not include

## Clients

Output client-specific setup docs to `docs/sources/clients/`.

One article per client. Each covers prerequisites, config file location, and a working server block.

### Claude Desktop

- **Context:** docs/clients/claude-desktop.md
- **Include:** Config path by OS, JSON example, uvx and binary options.
- **Exclude:** General MCP theory.

### Cursor

- **Context:** docs/clients/cursor.md
- **Include:** Global vs project mcp.json, UI and manual config.
- **Exclude:** Other clients.

### Other clients (Codex, VS Code Copilot, Windsurf, Zed, Claude Code, Gemini CLI)

- **Context:** docs/clients/*.md for each
- **Include:** Same pattern – prerequisites, config path, example block.
- **Exclude:** Duplicating README installation.

## Set up

Output setup docs to `docs/sources/set-up/`.

Create individual articles for each setup method (install and run the server).
Order articles from fewer steps to more involved (uvx, Docker, binary, Helm).

### Install with uvx

- **Context:** README.md (Quick Start, Usage), cmd/
- **Include:** Prerequisite (uv), minimal JSON config, GRAFANA_URL and GRAFANA_SERVICE_ACCOUNT_TOKEN. Grafana Cloud URL example.
- **Exclude:** All other install methods, deep configuration.

### Install with Docker

- **Context:** README.md (Docker image, STDIO vs SSE vs streamable-http)
- **Include:** docker pull/run for stdio and SSE, port mapping, env vars. Override `-t stdio` for stdio mode.
- **Exclude:** Helm, binary, TLS server config (cover in Configure).

### Install the binary

- **Context:** README.md (Download binary, Build from source)
- **Include:** Releases page link, GOBIN/path, `go install` for source build.
- **Exclude:** uvx and Docker steps.

### Deploy with Helm

- **Context:** README.md (Helm chart)
- **Include:** helm repo add/install, grafana.apiKey and grafana.url. Link to helm-charts repo.
- **Exclude:** In-cluster Grafana setup (out of scope).

## Configure

Output configure docs to `docs/sources/configure/`.

Create individual articles for each feature or component.
Order articles from foundational to advanced configuration.

### Authentication

- **Context:** README.md (Usage, service account, env vars)
- **Include:** Service account token (recommended), username/password, GRAFANA_SERVICE_ACCOUNT_TOKEN and GRAFANA_API_KEY deprecation. Link to Grafana service account docs.
- **Exclude:** TLS client certs (separate article).

### Transports and addresses

- **Context:** README.md (CLI Flags Reference, Transport Options)
- **Include:** stdio (default for local), sse, streamable-http; --address, --base-path, --endpoint-path. When to use each.
- **Exclude:** Client config examples (covered in Set up / client docs).

### Enable and disable tools

- **Context:** README.md (Tool Configuration, --enabled-tools, --disable-*)
- **Include:** --enabled-tools for runpanelquery, examples, clickhouse, cloudwatch, etc. --disable-* by category. Read-only mode (--disable-write).
- **Exclude:** Full tool list (refer to README or introduction).

### Client TLS (Grafana connection)

- **Context:** README.md (TLS Configuration, Client TLS)
- **Include:** --tls-cert-file, --tls-key-file, --tls-ca-file, --tls-skip-verify. Env-based config not required for this article.
- **Exclude:** Server TLS (streamable-http only).

### Server TLS (streamable-http)

- **Context:** README.md (Server TLS Configuration)
- **Include:** --server.tls-cert-file, --server.tls-key-file. When and why (HTTPS for MCP server).
- **Exclude:** Client TLS, Docker internal networking.

### Multi-organization and headers

- **Context:** README.md (Multi-Organization Support, Custom HTTP Headers)
- **Include:** GRAFANA_ORG_ID, X-Grafana-Org-Id; GRAFANA_EXTRA_HEADERS JSON.
- **Exclude:** RBAC (covered in introduction or separate doc if needed).

## introduction.md

Create one `introduction.md` article covering these key unique concepts:

- **Model Context Protocol (MCP):** What MCP is and how the Grafana MCP server lets AI assistants and LLM clients talk to Grafana (dashboards, datasources, metrics, logs, traces, alerts, incidents).
- **Tools and capabilities:** High-level categories (dashboards, datasources, Prometheus/Loki/others, alerting, incidents, OnCall, Sift, navigation, rendering). Configurable tool set and context-window considerations.
- **Authentication and RBAC:** Service account (or user) and Grafana RBAC; least-privilege vs Editor role; link to Grafana RBAC docs.

- **Context:** README.md (Features, Requirements, RBAC sections)
- **Include:** One-page overview for customers; link to set-up and configure; mention Grafana 9.0+.
- **Exclude:** Step-by-step setup, full CLI reference (link to README or configure).

## Guides

Output guides docs to `docs/sources/guides/`.

Include 5-10 critical use case articles.

Show the user how to use the product and its features to solve problems.

Create individual articles for each use case.
Order articles from foundational to advanced use cases.

### Query metrics with Prometheus

- **Context:** README.md (Prometheus Querying), MCP tool descriptions
- **Include:** Use the MCP server from a client to run PromQL (instant/range), list metrics and labels. Example use case (e.g., check a metric in a conversation).
- **Exclude:** Panel query execution, Loki or other datasources.

### Query logs with Loki

- **Context:** README.md (Loki Querying)
- **Include:** LogQL log and metric queries, label names/values, patterns. Example use case.
- **Exclude:** Search logs tool, ClickHouse.

### Search and inspect dashboards

- **Context:** README.md (Dashboards, Context Window Management)
- **Include:** Search dashboards, get summary vs full JSON, get panel queries; when to use get_dashboard_property.
- **Exclude:** Update/patch dashboard (separate guide if desired).

### Manage alert rules

- **Context:** README.md (Alerting)
- **Include:** List/fetch alert rules, create/update/delete; contact points and routing. RBAC requirements.
- **Exclude:** OnCall alert groups (different feature).

### Generate deeplinks to Grafana

- **Context:** README.md (Navigation)
- **Include:** Dashboard, panel, and Explore deeplinks; time range and variables. Use case: share accurate links from an AI assistant.
- **Exclude:** Rendering (get panel image).

### Run a dashboard panel query

- **Context:** README.md (Run Panel Query)
- **Include:** Enabling runpanelquery, executing a panel query with time range and variable overrides.
- **Exclude:** Writing new queries in Prometheus/Loki tools.

### Use Grafana Incident and Sift

- **Context:** README.md (Incidents, Sift Investigations)
- **Include:** List/create/update incidents; list investigations, find error patterns, find slow requests. Viewer vs Editor role.
- **Exclude:** OnCall schedules (separate).

## Developer

Output developer docs to `docs/sources/developer/`.

Cover public user SDKs and APIs.

Create individual articles for each SDK or API.
Put SDKs before APIs.
Order articles from most popular language or framework to least.

### Go SDK (programmatic use)

- **Context:** README.md (Programmatic Usage, TLS), pkg.go.dev reference
- **Include:** Using the library in Go; ComposedStdioContextFunc, GrafanaConfig, TLSConfig. Link to pkg.go.dev.
- **Exclude:** HTTP API of the server (no public HTTP API for end users).

### Observability (metrics and tracing)

- **Context:** README.md (Observability)
- **Include:** --metrics, --metrics-address; Prometheus metrics (mcp_server_operation_duration_seconds, etc.). OTEL env vars and tracing.
- **Exclude:** Grafana datasource setup for metrics (out of scope).
