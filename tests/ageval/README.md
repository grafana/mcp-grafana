# k6 ageval evals

A k6 port of the DeepEval alerting suite (`tests/alerting_test.py`), using the
experimental `k6/experimental/ageval` module. It runs the real `claude` CLI
headless with **this repo's** mcp-grafana binary as an MCP server, captures each
trajectory, and grades it — emitting standard k6 metrics so results show up in
Grafana Cloud dashboards.

It **reuses the same docker-compose Grafana** the Python e2e suite uses, so there
is no separate environment to stand up.

## What maps to what

| DeepEval (Python)                        | ageval (this script)                       |
| ---------------------------------------- | ------------------------------------------ |
| `run_llm_tool_loop` (litellm + MCP)      | `CliAgent` running `claude` + mcp-grafana  |
| `assert_tool_operation` / `MCPUseMetric` | `res.expectSequence()` (subset arg match)  |
| `GEval(criteria=…, threshold=0.5)`       | `judge(res, { rubric, threshold: 0.5 })`   |
| pytest asserts                           | `check(res, …)`                            |
| `assert_test(...)`                       | `options.thresholds`                       |

The `claude-code` adapter strips the `mcp__grafana__` prefix, so the tool names
in the script (`alerting_manage_rules`, `alerting_manage_routing`) are the
server's own names — identical to the Python suite.

## Prerequisites

1. **A k6 binary built with the ageval module.** It's a registered core
   experimental module, so a plain build of the k6 branch that has it works (no
   xk6 needed):

   ```sh
   git -C ../k6 rev-parse HEAD      # the branch with internal/.../experimental/ageval
   ( cd ../k6 && go build -o "$(go env GOPATH)/bin/k6" . )
   ```

2. **Seeded Grafana + this repo's server binary:**

   ```sh
   make run-test-services   # docker-compose: Grafana :3000 (admin/admin), provisioned fixtures
   make build               # produces ./dist/mcp-grafana, referenced by grafana.mcp.json
   ```

3. **`claude`** installed and logged in, and an Anthropic key for the judge.

## Run

From the repo root:

```sh
ANTHROPIC_API_KEY_JUDGE=sk-...  k6 run tests/ageval/alerting.test.js
```

or `make test-k6-eval`.

## Differences vs. the Python suite (by design)

- **Driver**: exercises mcp-grafana *as Claude Code uses it*, not via litellm.
  There is no model-parametrized arm (`gpt-4o` vs `claude`); to add a second
  agent, point a second `CliAgent` at e.g. `codex` (the `codex` adapter is
  built in) as another scenario.
- **Read-only only**: the write tests (`create`/`update`/`delete`) are omitted —
  they verify side effects with a follow-up MCP call, and the k6 script has no
  MCP client of its own.
- **Pass/fail gate**: `judge`/`expectSequence` emit metrics rather than throwing;
  the run fails via `thresholds`, not assertions. No `flaky(reruns=2)` — raise
  `iterations` or loosen the rate thresholds if runs are noisy.
```
