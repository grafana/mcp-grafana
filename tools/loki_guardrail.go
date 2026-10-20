// This file wires the opt-in Loki query cost guardrail into the Loki tools.
// The decision logic (selector scanning, range cap, index/stats byte budget)
// lives in the standalone lokiguard module so other callers can share it;
// this file owns what the MCP server does with a decision: logging, metrics,
// and the error returned to the agent.
package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"

	"github.com/grafana/mcp-grafana/lokiguard"
)

// warnUnknownGuardrailModeOnce gates the once-per-process warning for
// unrecognized guardrail modes. A variable (not a literal) so tests can
// reset it.
var warnUnknownGuardrailModeOnce sync.Once

// lokiGuardrailScopeHint names this server's discovery tools in the guidance
// returned to the agent.
const lokiGuardrailScopeHint = "use query_loki_stats or list_loki_label_names/list_loki_label_values to scope the query first"

// guardLokiQuery applies the opt-in Loki query cost guardrail to a Loki
// query before it reaches the datasource. Loki's own max_query_bytes_read
// limit is not enforced on log queries without a line filter, so a broad
// selector over a wide range can scan terabytes while passing every
// server-side check. The guardrail closes that gap: it requires selective
// stream selectors, bounds the effective time range (including range-vector
// durations like [30d]), and asks Loki's index/stats API for a byte estimate
// before admitting the query.
//
// In shadow mode violations are logged but the query proceeds; in enforce
// mode a plain error is returned, which ConvertTool surfaces to the client
// as a tool result with isError=true so LLM callers can rewrite and retry.
// Queries the guardrail cannot assess (unparseable, or a failed estimate) run
// unless LokiGuardrailOnEstimateFailure is "block".
func guardLokiQuery(ctx context.Context, backend lokiBackend, logql, queryType string, start, end time.Time) error {
	config := mcpgrafana.GrafanaConfigFromContext(ctx)
	mode := config.LokiGuardrailMode
	if mode == "" || mode == mcpgrafana.LokiGuardrailOff {
		return nil
	}
	// Unknown non-empty modes enforce (fail-closed); warn once per process
	// so library embedders that set the mode programmatically get a signal.
	// The CLI validates the mode at startup and never reaches this path.
	switch mode {
	case mcpgrafana.LokiGuardrailShadow, mcpgrafana.LokiGuardrailEnforce:
	default:
		warnUnknownGuardrailModeOnce.Do(func() {
			config.LoggerOrDefault().WarnContext(ctx, "unrecognized Loki guardrail mode; treating as enforce", "mode", mode)
		})
	}
	enforced := mode != mcpgrafana.LokiGuardrailShadow
	blockUnknown := config.LokiGuardrailOnEstimateFailure == mcpgrafana.LokiGuardrailFailureBlock

	// The byte estimate is only consulted on native Loki, where index/stats
	// is an index-only lookup. The VictoriaLogs QueryStats approximation
	// runs the selector as a real query, which would defeat the purpose.
	_, native := backend.(*lokiNativeBackend)
	var statsCalls, statsOK int
	var stats lokiguard.StatsFunc
	if native {
		stats = func(ctx context.Context, selector string, start, end time.Time) (int64, error) {
			statsCalls++
			s, err := backend.QueryStats(ctx, selector, start, end)
			if err != nil {
				return 0, err
			}
			statsOK++
			return s.Bytes, nil
		}
	}

	policy := lokiguard.Policy{
		MaxBytes: config.LokiGuardrailMaxBytes,
		MaxRange: config.LokiGuardrailMaxRange,
		// Size every query, including ones a static check already blocks, so
		// the estimated_bytes histogram shows the full distribution. Costs one
		// extra index/stats call per statically blocked query.
		EstimateAlways: true,
	}
	d := lokiguard.Assess(ctx, lokiguard.Query{
		LogQL:   logql,
		Start:   start,
		End:     end,
		Instant: queryType == "instant",
	}, policy, stats)

	metrics := lokiGuardrailInstrumentsFor(config.MeterProviderOrDefault())
	backendLabel := guardrailBackendLabel(backend)
	logger := config.LoggerOrDefault()

	// An estimate exists when the byte check produced a total (it may be a
	// genuine zero: Loki reports 0 for data not yet flushed to the store).
	if d.EstimatedBytes > 0 || (statsCalls > 0 && statsCalls == statsOK) {
		metrics.recordEstimatedBytes(ctx, backendLabel, d.Verdict, d.EstimatedBytes)
	}

	switch d.Verdict {
	case lokiguard.Allow:
		metrics.recordAdmitted(ctx, backendLabel)
		return nil

	case lokiguard.Unknown:
		cause := string(d.Cause)
		if d.Cause == lokiguard.CauseUnparseable {
			// Warn on native Loki so shadow-mode rollouts can spot query
			// shapes the scanner misses; on other backends (VictoriaLogs),
			// where brace-less queries are the normal shape, log at debug to
			// avoid per-query noise.
			msg := "loki guardrail found no parseable stream selector; passing query through (fail-open)"
			if blockUnknown {
				msg = "loki guardrail found no parseable stream selector"
			}
			if native {
				logger.WarnContext(ctx, msg)
			} else {
				logger.DebugContext(ctx, msg)
			}
			logger.DebugContext(ctx, "loki guardrail query detail", "logql", logql)
		} else {
			msg := "loki guardrail byte estimate failed (fail-open)"
			if blockUnknown {
				msg = "loki guardrail byte estimate failed"
			}
			logger.WarnContext(ctx, msg, "error", d.Err)
		}
		if !blockUnknown {
			metrics.recordFailOpen(ctx, backendLabel, cause)
			return nil
		}
		// Block-on-unknown reuses the blocked/would_block counters with the
		// cause as the reason, so unassessable queries are counted once.
		metrics.recordBlocked(ctx, enforced, backendLabel, cause)
		if !enforced {
			logger.WarnContext(ctx, "loki guardrail would block unassessable query", "cause", cause)
			return nil
		}
		logger.WarnContext(ctx, "loki guardrail blocked unassessable query", "cause", cause)
		return unassessableError(d)

	default: // lokiguard.Block
		reasons := make([]string, len(d.Reasons))
		for i, r := range d.Reasons {
			reasons[i] = r.Message
		}
		metrics.recordBlocked(ctx, enforced, backendLabel, string(d.Reasons[0].Kind))

		// Log the extracted selectors (the basis of the decision) rather than
		// the full LogQL, which may embed sensitive literals in line filters;
		// the full query is available at debug level.
		logger.DebugContext(ctx, "loki guardrail query detail", "logql", logql)
		if !enforced {
			logger.WarnContext(ctx, "loki guardrail would block query", "reasons", strings.Join(reasons, "; "), "selectors", strings.Join(d.Selectors, " "))
			return nil
		}
		logger.WarnContext(ctx, "loki guardrail blocked query", "reasons", strings.Join(reasons, "; "), "selectors", strings.Join(d.Selectors, " "))
		return errors.New(lokiguard.Guidance(d, policy, lokiGuardrailScopeHint))
	}
}

// unassessableError is the enforce-mode error for a query the guardrail could
// not assess when LokiGuardrailOnEstimateFailure is "block".
func unassessableError(d lokiguard.Decision) error {
	if d.Cause == lokiguard.CauseUnparseable {
		return fmt.Errorf("query blocked by the Loki cost guardrail: it has no stream selector the guardrail can assess. " +
			"Rewrite it as a plain LogQL query with a stream selector such as {namespace=\"x\", app=\"y\"}, " +
			lokiGuardrailScopeHint)
	}
	return fmt.Errorf("query blocked by the Loki cost guardrail: its cost could not be estimated (Loki index/stats failed: %v). "+
		"Retry shortly, or narrow the selector and time range; %s", d.Err, lokiGuardrailScopeHint)
}
