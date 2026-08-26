package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// listAgento11yExperiments lists the experiments of the tenant. Every tag is
// added rather than set, because the API reads tag as a repeated parameter and
// matches an experiment carrying any of them.
func (c *Client) listAgento11yExperiments(ctx context.Context, r agento11yExperimentReadRequest) (*agento11yListResponse[Agento11yExperiment], error) {
	query := agento11yPageQuery(r.Limit, r.Cursor)
	setAgento11yParam(query, "suite_id", r.SuiteID)
	setAgento11yParam(query, "status", r.Status)
	setAgento11yParam(query, "source", r.Source)
	setAgento11yParam(query, "created_by", r.CreatedBy)
	setAgento11yParam(query, "order", r.Order)
	for _, tag := range r.Tag {
		if tag != "" {
			query.Add("tag", tag)
		}
	}

	from, to, err := agento11yTimeWindow("from", r.From, "to", r.To)
	if err != nil {
		return nil, err
	}
	completedFrom, completedTo, err := agento11yTimeWindow("completed_from", r.CompletedFrom, "completed_to", r.CompletedTo)
	if err != nil {
		return nil, err
	}
	setAgento11yTimeParam(query, "from", from)
	setAgento11yTimeParam(query, "to", to)
	setAgento11yTimeParam(query, "completed_from", completedFrom)
	setAgento11yTimeParam(query, "completed_to", completedTo)

	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yExperiment]](ctx, c, http.MethodGet, "/eval/experiments", query, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// setAgento11yParam sets a filter, skipping an empty value so an unset filter
// is not sent as an empty string the API would reject.
func setAgento11yParam(query url.Values, name, value string) {
	if value != "" {
		query.Set(name, value)
	}
}

// setAgento11yTimeParam sets an RFC3339 bound, skipping a zero time so an
// unsupplied bound is not sent as the zero instant.
func setAgento11yTimeParam(query url.Values, name string, value time.Time) {
	if !value.IsZero() {
		query.Set(name, value.Format(time.RFC3339))
	}
}

func (c *Client) getAgento11yExperiment(ctx context.Context, experimentID string) (*Agento11yExperiment, error) {
	resp, err := fetchAgento11yJSON[Agento11yExperiment](ctx, c, http.MethodGet, agento11yExperimentPath(experimentID), nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func agento11yExperimentPath(experimentID string) string {
	return "/eval/experiments/" + url.PathEscape(experimentID)
}

// agento11yTrialPath builds the path of one test case trial. Trials are
// addressed by their own top-level route, not under the experiment.
func agento11yTrialPath(trialID string) string {
	return "/eval/test-case-trials/" + url.PathEscape(trialID)
}

// getAgento11yExperimentReport fetches the full report and returns the compact
// form. The whole report crosses the wire either way, so a report above the
// response limit fails here rather than arriving trimmed.
func (c *Client) getAgento11yExperimentReport(ctx context.Context, experimentID string, rowLimit int) (*Agento11yCompactExperimentReport, error) {
	report, err := fetchAgento11yJSON[Agento11yExperimentReport](ctx, c, http.MethodGet, agento11yExperimentPath(experimentID)+"/report", nil, nil)
	if err != nil {
		return nil, err
	}
	return compactAgento11yReport(report, rowLimit), nil
}

// compactAgento11yReport drops the payloads that have a dedicated drill-down
// operation: the free-form test case input and expected values, and the score
// and artifact records behind their counts. A failed trial keeps its error
// message, so a failing row states its reason without a drill-down.
func compactAgento11yReport(report Agento11yExperimentReport, rowLimit int) *Agento11yCompactExperimentReport {
	if rowLimit <= 0 {
		rowLimit = defaultAgento11yPageSize
	}
	if rowLimit > agento11yMaxReportRows {
		rowLimit = agento11yMaxReportRows
	}

	kept := report.Rows
	if len(kept) > rowLimit {
		kept = kept[:rowLimit]
	}

	rows := make([]Agento11yCompactReportRow, 0, len(kept))
	for _, row := range kept {
		compact := Agento11yCompactReportRow{
			TestCaseID: row.TestCaseID,
			Summary:    row.Summary,
			Trials:     make([]Agento11yCompactReportTrial, 0, len(row.Trials)),
		}
		if row.TestCaseSnapshot != nil {
			compact.Name = row.TestCaseSnapshot.Name
			compact.Category = row.TestCaseSnapshot.Category
			compact.Tags = row.TestCaseSnapshot.Tags
		}
		for _, result := range row.Trials {
			compact.Trials = append(compact.Trials, Agento11yCompactReportTrial{
				TrialID:        result.Trial.TrialID,
				Attempt:        result.Trial.Attempt,
				Status:         result.Trial.Status,
				FinalScore:     compactAgento11yScore(result.FinalScore),
				Cost:           result.Trial.Cost,
				DurationMS:     result.Trial.DurationMS,
				TotalTokens:    result.Trial.TotalTokens,
				ConversationID: result.Trial.ConversationID,
				TraceID:        result.Trial.TraceID,
				Error:          result.Trial.Error,
				ScoreCount:     len(result.Scores),
				ArtifactCount:  len(result.Artifacts),
			})
		}
		rows = append(rows, compact)
	}

	return &Agento11yCompactExperimentReport{
		Experiment:    report.Experiment,
		Summary:       report.Summary,
		Rows:          rows,
		TotalRowCount: len(report.Rows),
		RowsTruncated: len(rows) < len(report.Rows),
	}
}

func compactAgento11yScore(score *Agento11yEvalScore) *Agento11yCompactScore {
	if score == nil {
		return nil
	}
	return &Agento11yCompactScore{
		EvaluatorID: score.EvaluatorID,
		ScoreKey:    score.ScoreKey,
		ScoreType:   score.ScoreType,
		Value:       score.Value,
		Passed:      score.Passed,
	}
}

func (c *Client) listAgento11yExperimentTrials(ctx context.Context, experimentID string, limit int, cursor string) (*agento11yListResponse[Agento11yTestCaseTrial], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yTestCaseTrial]](ctx, c, http.MethodGet, agento11yExperimentPath(experimentID)+"/trials", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) listAgento11yExperimentScores(ctx context.Context, experimentID string, limit int, cursor string) (*agento11yListResponse[Agento11yEvalScore], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yEvalScore]](ctx, c, http.MethodGet, agento11yExperimentPath(experimentID)+"/scores", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) getAgento11yTrial(ctx context.Context, trialID string) (*Agento11yTestCaseTrial, error) {
	resp, err := fetchAgento11yJSON[Agento11yTestCaseTrial](ctx, c, http.MethodGet, agento11yTrialPath(trialID), nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) listAgento11yTrialScores(ctx context.Context, trialID string, limit int, cursor string) (*agento11yListResponse[Agento11yEvalScore], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yEvalScore]](ctx, c, http.MethodGet, agento11yTrialPath(trialID)+"/scores", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// listAgento11yTrialArtifacts returns artifact metadata only. The bytes live
// behind a separate content route this tool does not expose.
func (c *Client) listAgento11yTrialArtifacts(ctx context.Context, trialID string, limit int, cursor string) (*agento11yListResponse[Agento11yArtifact], error) {
	resp, err := fetchAgento11yJSON[agento11yListResponse[Agento11yArtifact]](ctx, c, http.MethodGet, agento11yTrialPath(trialID)+"/artifacts", agento11yPageQuery(limit, cursor), nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) listAgento11yExperimentFacets(ctx context.Context, r agento11yExperimentReadRequest) (*Agento11yExperimentFacets, error) {
	query := url.Values{}
	setAgento11yParam(query, "source", r.Source)
	from, to, err := agento11yTimeWindow("from", r.From, "to", r.To)
	if err != nil {
		return nil, err
	}
	setAgento11yTimeParam(query, "from", from)
	setAgento11yTimeParam(query, "to", to)

	resp, err := fetchAgento11yJSON[Agento11yExperimentFacets](ctx, c, http.MethodGet, "/eval/experiment-facets", query, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// agento11yExperimentRead runs the experiment read operations shared by the
// read and read-write variants of agento11y_manage_experiments. Operations it
// does not handle return errAgento11yUnknownOperation.
func (c *Client) agento11yExperimentRead(ctx context.Context, operation string, r agento11yExperimentReadRequest) (any, error) {
	switch operation {
	case "list":
		return c.listAgento11yExperiments(ctx, r)
	case "get":
		return c.getAgento11yExperiment(ctx, r.ExperimentID)
	case "get_report":
		return c.getAgento11yExperimentReport(ctx, r.ExperimentID, r.RowLimit)
	case "list_trials":
		return c.listAgento11yExperimentTrials(ctx, r.ExperimentID, r.Limit, r.Cursor)
	case "list_scores":
		return c.listAgento11yExperimentScores(ctx, r.ExperimentID, r.Limit, r.Cursor)
	case "get_trial":
		return c.getAgento11yTrial(ctx, r.TrialID)
	case "list_trial_scores":
		return c.listAgento11yTrialScores(ctx, r.TrialID, r.Limit, r.Cursor)
	case "list_trial_artifacts":
		return c.listAgento11yTrialArtifacts(ctx, r.TrialID, r.Limit, r.Cursor)
	case "list_facets":
		return c.listAgento11yExperimentFacets(ctx, r)
	default:
		return nil, errAgento11yUnknownOperation
	}
}

func manageAgento11yExperimentsRead(ctx context.Context, args ManageAgento11yExperimentsReadParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_experiments: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	result, err := client.agento11yExperimentRead(ctx, args.Operation, args.readRequest())
	if errors.Is(err, errAgento11yUnknownOperation) {
		return nil, fmt.Errorf("agento11y_manage_experiments: unknown operation %q", args.Operation)
	}
	return result, err
}

// updateAgento11yExperiment patches an experiment. Only the supplied fields are
// sent, because the API reads them as optional pointers: an absent field is
// left unchanged while an explicitly empty description or tag list clears it.
// Status is not among them; a finished run rejects a status change with 409 and
// points at cancel.
func (c *Client) updateAgento11yExperiment(ctx context.Context, p ManageAgento11yExperimentsReadWriteParams) (*Agento11yExperiment, error) {
	body := map[string]any{}
	if p.Name != nil {
		body["name"] = *p.Name
	}
	if p.Description != nil {
		body["description"] = *p.Description
	}
	if p.Tags != nil {
		body["tags"] = *p.Tags
	}
	if p.Metadata != nil {
		body["metadata"] = p.Metadata
	}

	resp, err := fetchAgento11yJSON[Agento11yExperiment](ctx, c, http.MethodPatch, agento11yExperimentPath(p.ExperimentID), nil, body)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// cancelAgento11yExperiment stops a running experiment. The plugin proxy
// forwards this POST only when the last path segment ends in ":cancel", and
// url.PathEscape leaves a colon alone, so the action is appended after the ID
// is escaped and never comes from the caller.
func (c *Client) cancelAgento11yExperiment(ctx context.Context, experimentID string) (*Agento11yExperiment, error) {
	resp, err := fetchAgento11yJSON[Agento11yExperiment](ctx, c, http.MethodPost, agento11yExperimentPath(experimentID)+":cancel", nil, nil)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// agento11yExperimentWrite runs the write operations of
// agento11y_manage_experiments. Operations it does not handle return
// errAgento11yUnknownOperation.
func (c *Client) agento11yExperimentWrite(ctx context.Context, operation string, p ManageAgento11yExperimentsReadWriteParams) (any, error) {
	switch operation {
	case "update":
		return c.updateAgento11yExperiment(ctx, p)
	case "cancel":
		return c.cancelAgento11yExperiment(ctx, p.ExperimentID)
	default:
		return nil, errAgento11yUnknownOperation
	}
}

func manageAgento11yExperimentsReadWrite(ctx context.Context, args ManageAgento11yExperimentsReadWriteParams) (any, error) {
	if err := args.validate(); err != nil {
		return nil, fmt.Errorf("agento11y_manage_experiments: %w", err)
	}

	client, err := newAgento11yClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Agent Observability client: %w", err)
	}

	result, err := client.agento11yExperimentRead(ctx, args.Operation, args.readRequest())
	if !errors.Is(err, errAgento11yUnknownOperation) {
		return result, err
	}

	result, err = client.agento11yExperimentWrite(ctx, args.Operation, args)
	if errors.Is(err, errAgento11yUnknownOperation) {
		return nil, fmt.Errorf("agento11y_manage_experiments: unknown operation %q", args.Operation)
	}
	return result, err
}
