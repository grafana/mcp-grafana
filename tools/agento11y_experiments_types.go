package tools

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Wire types for the offline experiments of the Agent Observability eval
// control plane (/eval/experiments, /eval/test-case-trials, and
// /eval/experiment-facets on the grafana-agento11y-app plugin resources proxy).

// agento11yMaxReportRows caps get_report's row_limit. 500 matches the page cap
// the eval list routes apply.
const agento11yMaxReportRows = 500

// Agento11yExperiment is one offline experiment run. The API marks source,
// collection_id, evaluators, and score_count as json:"-", so they never reach a
// client and are absent here.
type Agento11yExperiment struct {
	TenantID     string                        `json:"tenant_id,omitempty"`
	ExperimentID string                        `json:"experiment_id"`
	Name         string                        `json:"name"`
	Description  string                        `json:"description,omitempty"`
	Status       string                        `json:"status"` // running, completed, failed, canceled
	SuiteID      string                        `json:"suite_id,omitempty"`
	SuiteVersion string                        `json:"suite_version,omitempty"`
	Candidate    *Agento11yExperimentCandidate `json:"candidate,omitempty"`
	Tags         []string                      `json:"tags,omitempty"`
	Metadata     map[string]any                `json:"metadata,omitempty"`
	// PlannedTrialCount is set by the runner. A nil value means the plan size is
	// unknown, which is not the same as a planned zero.
	PlannedTrialCount *int                              `json:"planned_trial_count,omitempty"`
	ResultStatus      string                            `json:"result_status,omitempty"` // pending, ready, failed
	ResultError       string                            `json:"result_error,omitempty"`
	Result            *Agento11yExperimentReportSummary `json:"result,omitempty"`
	Error             string                            `json:"error,omitempty"`
	CreatedBy         string                            `json:"created_by,omitempty"`
	StartedAt         *time.Time                        `json:"started_at,omitempty"`
	CompletedAt       *time.Time                        `json:"completed_at,omitempty"`
	CreatedAt         time.Time                         `json:"created_at,omitzero"`
	UpdatedAt         time.Time                         `json:"updated_at,omitzero"`
}

// Agento11yExperimentCandidate identifies what was under test.
type Agento11yExperimentCandidate struct {
	AgentName     string `json:"agent_name,omitempty"`
	AgentVersion  string `json:"agent_version,omitempty"`
	PromptVersion string `json:"prompt_version,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`
	ModelName     string `json:"model_name,omitempty"`
	GitSHA        string `json:"git_sha,omitempty"`
}

// Agento11yExperimentReportSummary holds the experiment-wide aggregates. It is
// both the summary of a report and the result field of an experiment, so
// headline numbers are available from 'get' without fetching the report.
//
// The aggregates are pointers because the API omits one it cannot compute, and
// a decoded zero would report "0% passed" where the truth is "not known yet".
// token_coverage and cost_coverage say how much of the run the token and cost
// sums cover: none, partial, or complete.
type Agento11yExperimentReportSummary struct {
	TestCaseCount   int                `json:"test_case_count"`
	TrialCount      int                `json:"trial_count"`
	CompletedCount  int                `json:"completed_count"`
	FailedCount     int                `json:"failed_count"`
	CanceledCount   int                `json:"canceled_count"`
	PassRate        *float64           `json:"pass_rate,omitempty"`
	PassAtK         map[string]float64 `json:"pass_at_k,omitempty"`
	PassPowerK      map[string]float64 `json:"pass_power_k,omitempty"`
	FinalScoreAvg   *float64           `json:"final_score_avg,omitempty"`
	TotalCost       *float64           `json:"total_cost,omitempty"`
	TotalTokens     *int64             `json:"total_tokens,omitempty"`
	PassCount       int                `json:"pass_count"`
	PassDenominator int                `json:"pass_denominator"`
	FinalScoreSum   float64            `json:"final_score_sum"`
	FinalScoreCount int                `json:"final_score_count"`
	TokenCoverage   string             `json:"token_coverage,omitempty"`
	CostCoverage    string             `json:"cost_coverage,omitempty"`
}

// Agento11yTestCaseTrial is one attempt at one test case within an experiment.
//
// cost and the token counts are filled in by the report path, which falls back
// to the usage recorded on the conversation. The /trials route does no such
// fallback, so the same trial can carry them in a report and omit them in a
// list.
type Agento11yTestCaseTrial struct {
	TenantID       string                     `json:"tenant_id,omitempty"`
	TrialID        string                     `json:"trial_id"`
	ExperimentID   string                     `json:"experiment_id"`
	TestCaseID     string                     `json:"test_case_id"`
	TestCase       *Agento11yTestCaseSnapshot `json:"test_case,omitempty"`
	Attempt        int                        `json:"attempt"`
	Status         string                     `json:"status"` // running, completed, failed, canceled
	TraceID        string                     `json:"trace_id,omitempty"`
	SpanID         string                     `json:"span_id,omitempty"`
	ConversationID string                     `json:"conversation_id,omitempty"`
	Cost           *float64                   `json:"cost,omitempty"`
	InputTokens    *int64                     `json:"input_tokens,omitempty"`
	OutputTokens   *int64                     `json:"output_tokens,omitempty"`
	TotalTokens    *int64                     `json:"total_tokens,omitempty"`
	DurationMS     *int64                     `json:"duration_ms,omitempty"`
	Error          string                     `json:"error,omitempty"`
	Metadata       map[string]any             `json:"metadata,omitempty"`
	StartedAt      *time.Time                 `json:"started_at,omitempty"`
	CompletedAt    *time.Time                 `json:"completed_at,omitempty"`
	CreatedAt      time.Time                  `json:"created_at,omitzero"`
	UpdatedAt      time.Time                  `json:"updated_at,omitzero"`
}

// Agento11yTestCaseSnapshot is the test case as it stood when the trial ran,
// copied so a later suite edit cannot rewrite history. input and expected are
// free-form and are the largest part of a report.
type Agento11yTestCaseSnapshot struct {
	TestCaseID   string                 `json:"test_case_id"`
	SuiteID      string                 `json:"suite_id,omitempty"`
	SuiteVersion string                 `json:"suite_version,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Description  string                 `json:"description,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	Category     string                 `json:"category,omitempty"`
	Input        map[string]any         `json:"input,omitempty"`
	Expected     map[string]any         `json:"expected,omitempty"`
	Metadata     map[string]any         `json:"metadata,omitempty"`
	ArtifactRefs []Agento11yArtifactRef `json:"artifact_refs,omitempty"`
}

// Agento11yEvalScore is a score as the eval control plane serves it. It is not
// Agento11yScore: the /query routes serve a narrower shape with a nested source
// object, while these routes add the trial, test case, and grader links an
// offline run needs.
type Agento11yEvalScore struct {
	TenantID             string `json:"tenant_id,omitempty"`
	ScoreID              string `json:"score_id"`
	GenerationID         string `json:"generation_id,omitempty"`
	ConversationID       string `json:"conversation_id,omitempty"`
	TraceID              string `json:"trace_id,omitempty"`
	SpanID               string `json:"span_id,omitempty"`
	TrialID              string `json:"trial_id,omitempty"`
	TestCaseID           string `json:"test_case_id,omitempty"`
	GraderConversationID string `json:"grader_conversation_id,omitempty"`
	GraderGenerationID   string `json:"grader_generation_id,omitempty"`
	GraderTraceID        string `json:"grader_trace_id,omitempty"`
	EvaluatorID          string `json:"evaluator_id"`
	EvaluatorVersion     string `json:"evaluator_version"`
	EvaluatorDescription string `json:"evaluator_description,omitempty"`
	// EvaluatorRole records whether the evaluator ran as a gate or as an outcome
	// measure at the time the score was written.
	EvaluatorRole    string              `json:"evaluator_role,omitempty"`
	RuleID           string              `json:"rule_id,omitempty"`
	ExperimentID     string              `json:"experiment_id,omitempty"`
	ScoreKey         string              `json:"score_key"`
	ScoreType        string              `json:"score_type"` // number, bool, string
	Value            Agento11yScoreValue `json:"value"`
	Unit             string              `json:"unit,omitempty"`
	Passed           *bool               `json:"passed,omitempty"`
	Explanation      string              `json:"explanation,omitempty"`
	Metadata         map[string]any      `json:"metadata,omitempty"`
	CreatedAt        time.Time           `json:"created_at,omitzero"`
	IngestedAt       time.Time           `json:"ingested_at,omitzero"`
	SourceKind       string              `json:"source_kind,omitempty"`
	SourceID         string              `json:"source_id,omitempty"`
	AgentName        string              `json:"agent_name,omitempty"`
	EffectiveVersion string              `json:"effective_version,omitempty"`
}

// Agento11yArtifact is an artifact record. It carries a content_ref, never the
// bytes; fetching content is a separate route this tool does not expose.
type Agento11yArtifact struct {
	TenantID   string         `json:"tenant_id,omitempty"`
	ArtifactID string         `json:"artifact_id"`
	ParentKind string         `json:"parent_kind"`
	ParentID   string         `json:"parent_id"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"` // image, json, markdown, text, pdf, csv, binary
	Mime       string         `json:"mime,omitempty"`
	ContentRef string         `json:"content_ref"`
	SizeBytes  int64          `json:"size_bytes,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at,omitzero"`
	CreatedBy  string         `json:"created_by,omitempty"`
}

// Agento11yArtifactRef is the trimmed artifact form embedded in a test case.
type Agento11yArtifactRef struct {
	ArtifactID string `json:"artifact_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
}

// Agento11yExperimentFacets are the distinct filter values across a tenant's
// experiments. The route ignores suite, tag, status, and created_by so the
// option lists stay complete regardless of the filters already applied.
type Agento11yExperimentFacets struct {
	Owners []string `json:"owners"`
	Suites []string `json:"suites"`
	Tags   []string `json:"tags"`
}

// Agento11yExperimentReport is the raw response of GET
// /eval/experiments/{id}/report. It is never returned to a caller: get_report
// reshapes it into Agento11yCompactExperimentReport.
type Agento11yExperimentReport struct {
	Experiment Agento11yExperiment              `json:"experiment"`
	Summary    Agento11yExperimentReportSummary `json:"summary"`
	Rows       []Agento11yTestCaseResultRow     `json:"rows"`
}

// Agento11yTestCaseResultRow is one test case of a report, with every trial of
// that case.
type Agento11yTestCaseResultRow struct {
	TestCaseID       string                            `json:"test_case_id"`
	TestCaseSnapshot *Agento11yTestCaseSnapshot        `json:"test_case_snapshot,omitempty"`
	Summary          Agento11yTestCaseResultRowSummary `json:"summary"`
	Trials           []Agento11yTestCaseTrialResult    `json:"trials"`
}

// Agento11yTestCaseResultRowSummary aggregates one row. Its pass_at_k and
// pass_power_k are booleans per k, unlike the experiment-wide summary, where
// they are rates.
type Agento11yTestCaseResultRowSummary struct {
	TrialCount     int             `json:"trial_count"`
	CompletedCount int             `json:"completed_count"`
	PassAtK        map[string]bool `json:"pass_at_k,omitempty"`
	PassPowerK     map[string]bool `json:"pass_power_k,omitempty"`
	TrialPassRate  *float64        `json:"trial_pass_rate,omitempty"`
}

// Agento11yTestCaseTrialResult is one trial of a report row with every score
// and artifact attached. final_score is the score whose score_key is "final".
type Agento11yTestCaseTrialResult struct {
	Trial      Agento11yTestCaseTrial `json:"trial"`
	FinalScore *Agento11yEvalScore    `json:"final_score,omitempty"`
	Scores     []Agento11yEvalScore   `json:"scores"`
	Artifacts  []Agento11yArtifact    `json:"artifacts"`
}

// Agento11yCompactExperimentReport is what get_report returns: the headline
// numbers and the per-trial identifiers needed to drill down, and nothing else.
// The rows of Agento11yExperimentReport have no size bound.
type Agento11yCompactExperimentReport struct {
	Experiment    Agento11yExperiment              `json:"experiment"`
	Summary       Agento11yExperimentReportSummary `json:"summary"`
	Rows          []Agento11yCompactReportRow      `json:"rows"`
	TotalRowCount int                              `json:"total_row_count"`
	RowsTruncated bool                             `json:"rows_truncated"`
}

// Agento11yCompactReportRow is one report row with the test case snapshot
// reduced to the fields that identify it.
type Agento11yCompactReportRow struct {
	TestCaseID string                            `json:"test_case_id"`
	Name       string                            `json:"name,omitempty"`
	Category   string                            `json:"category,omitempty"`
	Tags       []string                          `json:"tags,omitempty"`
	Summary    Agento11yTestCaseResultRowSummary `json:"summary"`
	Trials     []Agento11yCompactReportTrial     `json:"trials"`
}

// Agento11yCompactReportTrial is one trial of a compact row. It carries the IDs
// the drill-down operations take, so a row worth investigating leads to
// get_trial, list_trial_scores, and list_trial_artifacts.
type Agento11yCompactReportTrial struct {
	TrialID        string                 `json:"trial_id"`
	Attempt        int                    `json:"attempt"`
	Status         string                 `json:"status"`
	FinalScore     *Agento11yCompactScore `json:"final_score,omitempty"`
	Cost           *float64               `json:"cost,omitempty"`
	DurationMS     *int64                 `json:"duration_ms,omitempty"`
	TotalTokens    *int64                 `json:"total_tokens,omitempty"`
	ConversationID string                 `json:"conversation_id,omitempty"`
	TraceID        string                 `json:"trace_id,omitempty"`
	// Error is kept because it is the only field that says why a trial failed,
	// and it is bounded: a runner writes one message, not a payload.
	Error string `json:"error,omitempty"`
	// ScoreCount and ArtifactCount stand in for the records themselves. A trial
	// whose final_score is absent may still carry scores, and without the count
	// that reads as an unscored trial.
	ScoreCount    int `json:"score_count"`
	ArtifactCount int `json:"artifact_count"`
}

// Agento11yCompactScore is a score reduced to its verdict. The explanation is
// dropped because a judge writes prose and a report holds one per trial;
// list_trial_scores returns the score in full.
type Agento11yCompactScore struct {
	EvaluatorID string              `json:"evaluator_id,omitempty"`
	ScoreKey    string              `json:"score_key,omitempty"`
	ScoreType   string              `json:"score_type,omitempty"`
	Value       Agento11yScoreValue `json:"value"`
	Passed      *bool               `json:"passed,omitempty"`
}

// agento11yExperimentFields are the filter and pagination parameters shared by
// the read and read-write variants of agento11y_manage_experiments. The ID and
// body parameters are declared on each variant separately, because their
// guidance names the operations that use them and the read variant must not
// advertise operations it rejects.
type agento11yExperimentFields struct {
	SuiteID   string `json:"suite_id,omitempty" jsonschema:"description=Test suite ID filter (for 'list')"`
	Status    string `json:"status,omitempty" jsonschema:"enum=running,enum=completed,enum=failed,enum=canceled,description=Experiment status filter (for 'list'). A finished run is 'completed'; there is no 'succeeded' status."`
	Source    string `json:"source,omitempty" jsonschema:"enum=collection,enum=external,description=Experiment origin filter: 'collection' for runs built from a saved collection\\, 'external' for runs reported by an SDK runner (for 'list' and 'list_facets')"`
	CreatedBy string `json:"created_by,omitempty" jsonschema:"description=Owner filter\\, matched against the values 'list_facets' reports under owners (for 'list')"`
	// Named tag, not tags, so it does not collide with the replacement tag list
	// the read-write variant declares for 'update'. Repeating it is what the API
	// expects, and each value becomes its own tag= query parameter.
	Tag           []string `json:"tag,omitempty" jsonschema:"description=Experiment tag filter (for 'list'). Several tags match an experiment carrying any of them\\, not all of them."`
	From          string   `json:"from,omitempty" jsonschema:"description=Start of the created_at window in RFC3339 or relative format (e.g. now-7d)\\, for 'list' and 'list_facets'"`
	To            string   `json:"to,omitempty" jsonschema:"description=End of the created_at window in RFC3339 or relative format (e.g. now)\\, for 'list' and 'list_facets'"`
	CompletedFrom string   `json:"completed_from,omitempty" jsonschema:"description=Start of the completed_at window in RFC3339 or relative format (for 'list'). 'from' and 'to' filter created_at; this pair filters completed_at instead\\, so it drops runs that have not finished."`
	CompletedTo   string   `json:"completed_to,omitempty" jsonschema:"description=End of the completed_at window in RFC3339 or relative format (for 'list')"`
	Order         string   `json:"order,omitempty" jsonschema:"enum=created_at_desc,enum=completed_at_desc,description=Sort order for 'list'\\, newest first either way. Defaults to 'created_at_desc'. 'completed_at_desc' returns a single page and cannot be combined with a cursor."`
	Limit         int      `json:"limit,omitempty" jsonschema:"description=Maximum number of results per page (default 50\\, max 500) (for the paginated list operations)"`
	Cursor        string   `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous response's next_cursor\\, echoed back exactly and never constructed or incremented. Repeat the same filters as the first call\\, using absolute RFC3339 times."`
	RowLimit      int      `json:"row_limit,omitempty" jsonschema:"description=Maximum number of test case rows 'get_report' returns (default 50\\, max 500). The report route is not paginated\\, so this trims the returned rows and sets rows_truncated; it does not reduce what the plugin sends."`
}

// agento11yExperimentReadRequest is the input of an experiment read operation,
// assembled by either tool variant.
type agento11yExperimentReadRequest struct {
	agento11yExperimentFields

	ExperimentID string
	TrialID      string
}

// validateOperation validates the read operations of
// agento11y_manage_experiments. Operations it does not handle return
// errAgento11yUnknownOperation.
func (r agento11yExperimentReadRequest) validateOperation(operation string) error {
	switch operation {
	case "list":
		if err := r.validateListFilters(); err != nil {
			return err
		}
	case "list_facets":
		if err := r.rejectFacetFilters(); err != nil {
			return err
		}
		if err := validateAgento11yExperimentSource(r.Source); err != nil {
			return err
		}
	case "get", "get_report", "list_trials", "list_scores":
		if r.ExperimentID == "" {
			return fmt.Errorf("experiment_id is required for %q operation", operation)
		}
	case "get_trial", "list_trial_scores", "list_trial_artifacts":
		if r.TrialID == "" {
			return fmt.Errorf("trial_id is required for %q operation", operation)
		}
	default:
		return errAgento11yUnknownOperation
	}
	return r.rejectUnusedFilters(operation)
}

// rejectUnusedFilters refuses a filter on an operation whose route does not
// send it. The filters are declared once for every operation, so 'list_trials'
// with status reads like a trial filter while the route answers with every
// trial of the run. A trial carries the experiment status names, which makes
// that superset look like the narrowed answer, the same failure
// rejectFacetFilters guards against. Pagination is left out: limit and cursor
// are either read by the route or harmless on one that returns a single object.
func (r agento11yExperimentReadRequest) rejectUnusedFilters(operation string) error {
	listOnly := []string{"list"}
	// The facets route narrows its own answer by source and the created_at window.
	listAndFacets := []string{"list", "list_facets"}

	field, readBy := agento11yFirstUnusedParam(operation, []agento11yParamUse{
		{name: "suite_id", set: r.SuiteID != "", operations: listOnly},
		{name: "status", set: r.Status != "", operations: listOnly},
		{name: "created_by", set: r.CreatedBy != "", operations: listOnly},
		{name: "tag", set: len(r.Tag) > 0, operations: listOnly},
		{name: "order", set: r.Order != "", operations: listOnly},
		{name: "completed_from", set: r.CompletedFrom != "", operations: listOnly},
		{name: "completed_to", set: r.CompletedTo != "", operations: listOnly},
		{name: "source", set: r.Source != "", operations: listAndFacets},
		{name: "from", set: r.From != "", operations: listAndFacets},
		{name: "to", set: r.To != "", operations: listAndFacets},
		{name: "row_limit", set: r.RowLimit != 0, operations: []string{"get_report"}},
	})
	if field == "" {
		return nil
	}
	return agento11yUnusedParamError(field, operation, readBy)
}

// validateListFilters rejects the filter combinations the API answers 400 for,
// so the call fails with the rule spelled out instead of a bare status.
func (r agento11yExperimentReadRequest) validateListFilters() error {
	if err := validateAgento11yExperimentStatus(r.Status); err != nil {
		return err
	}
	if err := validateAgento11yExperimentSource(r.Source); err != nil {
		return err
	}
	switch r.Order {
	case "", "created_at_desc":
	case "completed_at_desc":
		if r.Cursor != "" {
			return fmt.Errorf("order 'completed_at_desc' cannot be combined with a cursor: that ordering returns a single page and always answers with an empty next_cursor, so narrow the window with completed_from and completed_to instead")
		}
	default:
		return fmt.Errorf("unknown order %q, must be one of: created_at_desc, completed_at_desc", r.Order)
	}
	// The time bounds are not parsed here: the client methods resolve them
	// through agento11yTimeWindow before they issue a request, so a malformed or
	// reversed bound is reported from there.
	return r.rejectDriftingCursorBounds()
}

// rejectDriftingCursorBounds refuses a cursor paired with a relative bound. The
// bound re-resolves on every call, which moves the window the cursor was issued
// against and makes the next page skip or repeat rows.
func (r agento11yExperimentReadRequest) rejectDriftingCursorBounds() error {
	if r.Cursor == "" {
		return nil
	}
	for _, bound := range []struct{ name, value string }{
		{"from", r.From},
		{"to", r.To},
		{"completed_from", r.CompletedFrom},
		{"completed_to", r.CompletedTo},
	} {
		if agento11yRelativeTimeBound(bound.value) {
			return fmt.Errorf("paginating with a cursor requires repeating the first page's time bounds as absolute RFC3339 times: %s=%q is relative and re-resolves between calls, which moves the window the cursor was issued against", bound.name, bound.value)
		}
	}
	return nil
}

// rejectFacetFilters refuses a filter the facets route ignores. Dropping it
// silently would answer with the options across the whole tenant while looking
// like the narrowed answer the caller asked for. Pagination is not rejected:
// the route returns three plain lists, so a limit or cursor is a harmless
// mistake rather than a wrong answer.
func (r agento11yExperimentReadRequest) rejectFacetFilters() error {
	for _, ignored := range []struct {
		name string
		set  bool
	}{
		{"suite_id", r.SuiteID != ""},
		{"status", r.Status != ""},
		{"created_by", r.CreatedBy != ""},
		{"tag", len(r.Tag) > 0},
		{"completed_from", r.CompletedFrom != ""},
		{"completed_to", r.CompletedTo != ""},
	} {
		if ignored.set {
			return fmt.Errorf("%s is not read by 'list_facets', which reports the options across every experiment in the tenant; only source, from, and to narrow it", ignored.name)
		}
	}
	return nil
}

// agento11yTimeWindow parses a pair of time bounds and rejects a reversed
// window, which the API answers 400 for. A zero time means the bound was not
// supplied and must not be sent.
func agento11yTimeWindow(fromName, from, toName, to string) (time.Time, time.Time, error) {
	start, err := parseStartTime(from)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing %s: %w", fromName, err)
	}
	end, err := parseEndTime(to)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing %s: %w", toName, err)
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("%s must not be after %s", fromName, toName)
	}
	return start, end, nil
}

func validateAgento11yExperimentStatus(status string) error {
	switch status {
	case "", "running", "completed", "failed", "canceled":
		return nil
	default:
		return fmt.Errorf("unknown status %q, must be one of: running, completed, failed, canceled", status)
	}
}

func validateAgento11yExperimentSource(source string) error {
	switch source {
	case "", "collection", "external":
		return nil
	default:
		return fmt.Errorf("unknown source %q, must be one of: collection, external", source)
	}
}

const agento11yExperimentReadOperations = "list, get, get_report, list_trials, list_scores, get_trial, list_trial_scores, list_trial_artifacts, list_facets"

const agento11yExperimentAllOperations = agento11yExperimentReadOperations + ", update, cancel"

// ManageAgento11yExperimentsReadParams is the param struct for the read-only
// version of agento11y_manage_experiments.
type ManageAgento11yExperimentsReadParams struct {
	agento11yExperimentFields

	Operation    string `json:"operation" jsonschema:"required,enum=list,enum=get,enum=get_report,enum=list_trials,enum=list_scores,enum=get_trial,enum=list_trial_scores,enum=list_trial_artifacts,enum=list_facets,description=The operation to perform: 'list' for the experiments in this tenant\\, 'get' for one experiment with its headline result\\, 'get_report' for the per-test-case breakdown\\, 'list_trials' for one experiment's trials page by page\\, 'list_scores' for every score in one experiment\\, 'get_trial' for one trial in full\\, 'list_trial_scores' for one trial's scores with their explanations\\, 'list_trial_artifacts' for one trial's artifact metadata\\, 'list_facets' for the distinct suites\\, owners\\, and tags to filter by"`
	ExperimentID string `json:"experiment_id,omitempty" jsonschema:"description=Experiment ID from 'list' (required for 'get'\\, 'get_report'\\, 'list_trials'\\, and 'list_scores')"`
	TrialID      string `json:"trial_id,omitempty" jsonschema:"description=Trial ID from 'list_trials' or 'get_report' (required for 'get_trial'\\, 'list_trial_scores'\\, and 'list_trial_artifacts')"`
}

func (p ManageAgento11yExperimentsReadParams) readRequest() agento11yExperimentReadRequest {
	return agento11yExperimentReadRequest{
		agento11yExperimentFields: p.agento11yExperimentFields,
		ExperimentID:              p.ExperimentID,
		TrialID:                   p.TrialID,
	}
}

func (p ManageAgento11yExperimentsReadParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if errors.Is(err, errAgento11yUnknownOperation) {
		return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yExperimentReadOperations)
	}
	return err
}

// ManageAgento11yExperimentsReadWriteParams is the param struct for the
// read-write version of agento11y_manage_experiments.
type ManageAgento11yExperimentsReadWriteParams struct {
	agento11yExperimentFields

	Operation    string `json:"operation" jsonschema:"required,enum=list,enum=get,enum=get_report,enum=list_trials,enum=list_scores,enum=get_trial,enum=list_trial_scores,enum=list_trial_artifacts,enum=list_facets,enum=update,enum=cancel,description=The operation to perform. Reads: 'list'\\, 'get'\\, 'get_report'\\, 'list_trials'\\, 'list_scores'\\, 'get_trial'\\, 'list_trial_scores'\\, 'list_trial_artifacts'\\, 'list_facets'. Writes: 'update' (PATCH the name\\, description\\, tags\\, or metadata of an experiment)\\, 'cancel' (stop a running experiment)"`
	ExperimentID string `json:"experiment_id,omitempty" jsonschema:"description=Experiment ID from 'list'. Required for 'get'\\, 'get_report'\\, 'list_trials'\\, 'list_scores'\\, 'update'\\, and 'cancel'."`
	TrialID      string `json:"trial_id,omitempty" jsonschema:"description=Trial ID from 'list_trials' or 'get_report' (required for 'get_trial'\\, 'list_trial_scores'\\, and 'list_trial_artifacts')"`

	Name        *string        `json:"name,omitempty" jsonschema:"description=New experiment name (for 'update'). An omitted field is left unchanged\\, and a blank name is rejected. A finished experiment accepts only description and tags\\, so renaming one returns 409."`
	Description *string        `json:"description,omitempty" jsonschema:"description=New experiment description (for 'update'). An omitted field is left unchanged and an explicitly empty string clears it."`
	Tags        *[]string      `json:"tags,omitempty" jsonschema:"description=Replacement tag list for 'update'. It overwrites the whole list rather than adding to it\\, so run 'get' first and send the existing tags plus the new ones; an explicitly empty array clears them. The tag filter for 'list' is the separate 'tag' parameter."`
	Metadata    map[string]any `json:"metadata,omitempty" jsonschema:"description=Replacement metadata object (for 'update'). A finished experiment rejects it with 409."`
}

func (p ManageAgento11yExperimentsReadWriteParams) readRequest() agento11yExperimentReadRequest {
	return agento11yExperimentReadRequest{
		agento11yExperimentFields: p.agento11yExperimentFields,
		ExperimentID:              p.ExperimentID,
		TrialID:                   p.TrialID,
	}
}

func (p ManageAgento11yExperimentsReadWriteParams) validate() error {
	err := p.readRequest().validateOperation(p.Operation)
	if !errors.Is(err, errAgento11yUnknownOperation) {
		if err != nil {
			return err
		}
		return p.rejectUpdateFields()
	}

	switch p.Operation {
	case "update":
		if p.ExperimentID == "" {
			return fmt.Errorf("experiment_id is required for 'update' operation")
		}
		if p.Name == nil && p.Description == nil && p.Tags == nil && p.Metadata == nil {
			return fmt.Errorf("at least one of name, description, tags, or metadata is required for 'update' operation (an omitted field is left unchanged)")
		}
		if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
			return fmt.Errorf("name must not be blank for 'update' operation (omit it to leave the name unchanged)")
		}
		return p.rejectUnusedTrialID()
	case "cancel":
		if p.ExperimentID == "" {
			return fmt.Errorf("experiment_id is required for 'cancel' operation")
		}
		// The cancel route sends no body, so a field meant for 'update' is
		// dropped on the way out while the experiment still stops.
		if field := p.updateOnlyField(); field != "" {
			return fmt.Errorf("%s is not accepted by 'cancel' operation, which would drop it: it is only read by 'update'", field)
		}
		return p.rejectUnusedTrialID()
	default:
		return fmt.Errorf("unknown operation %q, must be one of: %s", p.Operation, agento11yExperimentAllOperations)
	}
}

// rejectUpdateFields refuses an 'update' body field on a read operation. This
// variant declares the fields for 'update', so a read operation would otherwise
// accept and drop them. 'tags' is the sharp one: it reads like the 'tag' filter,
// so 'list' or 'list_facets' with tags would answer across the whole tenant.
func (p ManageAgento11yExperimentsReadWriteParams) rejectUpdateFields() error {
	field := p.updateOnlyField()
	if field == "" {
		return nil
	}
	hint := ""
	if field == "tags" {
		hint = "; the tag filter for the read operations is the separate 'tag' parameter"
	}
	return fmt.Errorf("%s is only read by the 'update' operation, not by %q%s", field, p.Operation, hint)
}

// rejectUnusedTrialID refuses a trial ID on a write operation. Both write routes
// address an experiment, so a trial ID is dropped: 'cancel' with one reads as
// trial-scoped and stops every trial of the run instead, and still reports
// success.
func (p ManageAgento11yExperimentsReadWriteParams) rejectUnusedTrialID() error {
	if p.TrialID == "" {
		return nil
	}
	return agento11yUnusedParamError("trial_id", p.Operation, []string{"get_trial", "list_trial_scores", "list_trial_artifacts"})
}

// updateOnlyField reports the first body field that is set and that only the
// 'update' route sends.
func (p ManageAgento11yExperimentsReadWriteParams) updateOnlyField() string {
	switch {
	case p.Name != nil:
		return "name"
	case p.Description != nil:
		return "description"
	case p.Tags != nil:
		return "tags"
	case p.Metadata != nil:
		return "metadata"
	default:
		return ""
	}
}
