# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.11.6] - 2026-04-09

### Added

- Apply on-behalf-of authentication headers in the Grafana client transport chain, enabling delegated identity for API requests ([#728](https://github.com/grafana/mcp-grafana/pull/728))

### Fixed

- Preserve dashboard identity fields (`id`, `uid`, `version`) in patch mode to prevent accidental dashboard duplication ([#722](https://github.com/grafana/mcp-grafana/pull/722))

## [0.11.5] - 2026-04-09

### Added

- Forward selected request headers (e.g. `Cookie`, `Authorization`) to Grafana in SSE and streamable-http modes, enabling SSO and ALB session cookie authentication ([#659](https://github.com/grafana/mcp-grafana/pull/659))
- Support optional `projectName` parameter for Cloud Monitoring datasources to query specific GCP projects ([#710](https://github.com/grafana/mcp-grafana/pull/710))
- Add `BaseTransport` field to `GrafanaConfig` for custom HTTP transport composition ([#726](https://github.com/grafana/mcp-grafana/pull/726))

### Fixed

- Extract Elasticsearch query field in alert rule summaries for accurate rule descriptions ([#714](https://github.com/grafana/mcp-grafana/pull/714))
- Clarify dashboard authoring guidance in tool descriptions to reduce LLM confusion ([#713](https://github.com/grafana/mcp-grafana/pull/713))

## [0.11.4] - 2026-04-02

### Added

- Pyroscope series query and unified query tool for profiling data exploration ([#672](https://github.com/grafana/mcp-grafana/pull/672))
- Generic Kubernetes-style API client for interacting with Grafana app platform resources ([#690](https://github.com/grafana/mcp-grafana/pull/690))
- `--session-idle-timeout-minutes` CLI flag to automatically close idle sessions ([#691](https://github.com/grafana/mcp-grafana/pull/691))
- Accept `X-Grafana-Service-Account-Token` header as an alternative authentication method ([#280](https://github.com/grafana/mcp-grafana/pull/280))
- Server-side filtering for `alerting_manage_rules` list operation to reduce response size ([#629](https://github.com/grafana/mcp-grafana/pull/629))
- Support categorized labels from Loki >= 3.0 for better label discovery ([#671](https://github.com/grafana/mcp-grafana/pull/671))
- Gemini CLI extension configuration for Grafana URL, token, and org ID settings ([#665](https://github.com/grafana/mcp-grafana/pull/665))
- Automatically fetch public URL from Grafana frontend settings for more accurate deep links ([#664](https://github.com/grafana/mcp-grafana/pull/664))
- Support PromQL queries against Google Cloud Monitoring datasources ([#647](https://github.com/grafana/mcp-grafana/pull/647))
- Query metadata and configurable result limit for `query_loki_logs` ([#654](https://github.com/grafana/mcp-grafana/pull/654))

### Fixed

- Missing parameters in create/update alert rule tools now correctly exposed ([#663](https://github.com/grafana/mcp-grafana/pull/663))
- Handle `text/*` content-type responses from Grafana API instead of failing on non-JSON responses ([#694](https://github.com/grafana/mcp-grafana/pull/694))
- Prevent memory leaks in streamable-http mode by properly cleaning up resources ([#685](https://github.com/grafana/mcp-grafana/pull/685))
- Fallback between `/resources` and `/proxy` datasource endpoints for broader datasource compatibility ([#562](https://github.com/grafana/mcp-grafana/pull/562))
- Warn when org ID is missing instead of silently discarding the parameter ([#678](https://github.com/grafana/mcp-grafana/pull/678))
- Support multi-value dashboard variables in `get_panel_image` ([#677](https://github.com/grafana/mcp-grafana/pull/677))
- Return actionable errors for unsupported JSONPath syntax in dashboard patch operations ([#675](https://github.com/grafana/mcp-grafana/pull/675))
- Convert Prometheus POST requests to GET for compatibility with datasources that don't support POST ([#633](https://github.com/grafana/mcp-grafana/pull/633))
- Fix `generate_deeplink` for explore resource type ([#644](https://github.com/grafana/mcp-grafana/pull/644))
- Prevent bare boolean JSON Schema values that caused errors with some LLM providers ([#597](https://github.com/grafana/mcp-grafana/pull/597))
- Prevent nil pointer crash in `get_dashboard_panel_queries` ([#661](https://github.com/grafana/mcp-grafana/pull/661))

### Changed

- Enhance `ConvertTool` with flexible integer conversion for more robust type handling ([#631](https://github.com/grafana/mcp-grafana/pull/631))
- Use end time for Prometheus instant queries instead of defaulting to current time ([#683](https://github.com/grafana/mcp-grafana/pull/683))

### Security

- Validate ClickHouse identifiers to prevent SQL injection ([#693](https://github.com/grafana/mcp-grafana/pull/693))

## [0.11.3] - 2026-03-12

### Added

- Support panel filtering and template variable substitution in `get_dashboard_panel_queries` for more targeted query extraction ([#539](https://github.com/grafana/mcp-grafana/pull/539))
- New `alerting_manage_routing` tool for managing notification policies, contact points, and time intervals in a single unified tool ([#618](https://github.com/grafana/mcp-grafana/pull/618))
- Add `accountId` parameter to CloudWatch tools for cross-account monitoring support ([#616](https://github.com/grafana/mcp-grafana/pull/616))

### Fixed

- Add `OrgIDRoundTripper` to the Grafana client transport chain so organization ID is correctly sent on all requests ([#649](https://github.com/grafana/mcp-grafana/pull/649))

### Changed

- Consolidate alerting rule tools into a single `alerting_manage_rules` tool for simpler discovery ([#619](https://github.com/grafana/mcp-grafana/pull/619))
- Use typed struct for alert query parameters instead of untyped `models.AlertQuery` ([#630](https://github.com/grafana/mcp-grafana/pull/630))
- Add server-side filtering support to alerting client for more efficient rule queries (Grafana 10.0+) ([#612](https://github.com/grafana/mcp-grafana/pull/612))

## [0.11.2] - 2026-02-24

### Changed

- Optimize Docker builds with Go cross-compilation for faster multi-platform image builds ([#600](https://github.com/grafana/mcp-grafana/pull/600))
- Fix Python wheel subpackage build to use correct `--package-path` flag ([#601](https://github.com/grafana/mcp-grafana/pull/601))

## [0.11.1] - 2026-02-24

### Added

- New `run_panel_query` tool that executes dashboard panel queries directly, with support for Prometheus, Loki, ClickHouse, and CloudWatch datasources, template variable substitution, Grafana macro expansion, and batch multi-panel queries ([#542](https://github.com/grafana/mcp-grafana/pull/542))

### Changed

- Merge near-duplicate MCP tools to reduce overall tool count, making it easier for LLMs to select the right tool ([#596](https://github.com/grafana/mcp-grafana/pull/596))

## [0.11.0] - 2026-02-19

### Added

- Elasticsearch datasource support with Lucene and Query DSL syntax, time range filtering, and configurable result limits ([#424](https://github.com/grafana/mcp-grafana/pull/424))
- CloudWatch datasource support with namespace, metric, and dimension discovery tools plus a guided query workflow ([#536](https://github.com/grafana/mcp-grafana/pull/536))

### Fixed

- Support standard HTTP proxy environment variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`) for connecting through corporate proxies ([#578](https://github.com/grafana/mcp-grafana/pull/578))

## [0.10.0] - 2026-02-12

### Added

- ClickHouse datasource support ([#535](https://github.com/grafana/mcp-grafana/pull/535))
- `get_query_examples` tool for retrieving query examples from datasources ([#538](https://github.com/grafana/mcp-grafana/pull/538))
- `query_prometheus_histogram` tool for histogram percentile queries with automatic `histogram_quantile` PromQL generation ([#537](https://github.com/grafana/mcp-grafana/pull/537))
- Pagination support for `list_datasources` and `search_dashboards` tools with configurable limit/offset ([#543](https://github.com/grafana/mcp-grafana/pull/543))
- Custom HTTP headers support via `GRAFANA_EXTRA_HEADERS` environment variable for custom auth schemes and reverse proxy integration ([#522](https://github.com/grafana/mcp-grafana/pull/522))
- Prometheus metrics and OpenTelemetry instrumentation ([#506](https://github.com/grafana/mcp-grafana/pull/506))
- Alpine-based Docker image variants (`:alpine` and `:x.y.z-alpine` tags) for smaller image size (~74MB vs ~147MB) ([#568](https://github.com/grafana/mcp-grafana/pull/568))
- Support for `remove` operation on dashboard array elements by index ([#564](https://github.com/grafana/mcp-grafana/pull/564))

### Fixed

- `update_dashboard` tool descriptions and error messages improved to reduce LLM misuse ([#570](https://github.com/grafana/mcp-grafana/pull/570))
- Trim whitespace from dashboard patch operation paths ([#565](https://github.com/grafana/mcp-grafana/pull/565))
- DeepEval MCP evaluation for e2e tests ([#516](https://github.com/grafana/mcp-grafana/pull/516))

### Security

- Upgrade Docker base image packages to resolve critical OpenSSL CVE-2025-15467 (CVSS 9.8) ([#551](https://github.com/grafana/mcp-grafana/pull/551))

[0.11.6]: https://github.com/grafana/mcp-grafana/compare/v0.11.5...v0.11.6
[0.11.5]: https://github.com/grafana/mcp-grafana/compare/v0.11.4...v0.11.5
[0.11.4]: https://github.com/grafana/mcp-grafana/compare/v0.11.3...v0.11.4
[0.11.3]: https://github.com/grafana/mcp-grafana/compare/v0.11.2...v0.11.3
[0.11.2]: https://github.com/grafana/mcp-grafana/compare/v0.11.1...v0.11.2
[0.11.1]: https://github.com/grafana/mcp-grafana/compare/v0.11.0...v0.11.1
[0.11.0]: https://github.com/grafana/mcp-grafana/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/grafana/mcp-grafana/compare/v0.9.0...v0.10.0
