# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.10.0]: https://github.com/grafana/mcp-grafana/compare/v0.9.0...v0.10.0
