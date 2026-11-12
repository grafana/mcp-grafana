//go:build unit
// +build unit

package tools

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgento11yManageExperimentsRead(t *testing.T) {
	testCases := []struct {
		name        string
		params      ManageAgento11yExperimentsReadParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "list sends the default limit and no filters",
			params: ManageAgento11yExperimentsReadParams{Operation: "list"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiments", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
				require.Equal(t, "50", r.URL.Query().Get("limit"))
				for _, absent := range []string{"cursor", "suite_id", "status", "source", "created_by", "tag", "order", "from", "to", "completed_from", "completed_to"} {
					require.False(t, r.URL.Query().Has(absent), "%s should not be sent when unset", absent)
				}

				writeJSONResponse(t, w, `{"items":[{
					"tenant_id": "tenant-1",
					"experiment_id": "exp-1",
					"name": "nightly regression",
					"status": "completed",
					"suite_id": "suite-1",
					"suite_version": "v3",
					"tags": ["critical"],
					"candidate": {"agent_name": "triage", "model_name": "gpt-5", "git_sha": "abc123"},
					"planned_trial_count": 20,
					"result_status": "ready",
					"result": {"test_case_count": 10, "trial_count": 20, "completed_count": 20, "pass_rate": 0.85, "total_tokens": 41000, "token_coverage": "complete"},
					"created_at": "2026-01-02T03:04:05Z"
				}],"next_cursor":"42"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yExperiment])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "42", resp.NextCursor)

				experiment := resp.Items[0]
				assert.Equal(t, "exp-1", experiment.ExperimentID)
				assert.Equal(t, "completed", experiment.Status)
				assert.Equal(t, "suite-1", experiment.SuiteID)
				assert.Equal(t, []string{"critical"}, experiment.Tags)
				require.NotNil(t, experiment.Candidate)
				assert.Equal(t, "triage", experiment.Candidate.AgentName)
				require.NotNil(t, experiment.PlannedTrialCount)
				assert.Equal(t, 20, *experiment.PlannedTrialCount)

				// The headline aggregates come free with a list row, so finding a
				// regression needs no report fetch.
				require.NotNil(t, experiment.Result)
				require.NotNil(t, experiment.Result.PassRate)
				assert.InDelta(t, 0.85, *experiment.Result.PassRate, 1e-9)
				require.NotNil(t, experiment.Result.TotalTokens)
				assert.Equal(t, int64(41000), *experiment.Result.TotalTokens)
			},
		},
		{
			name: "list keeps every tag as its own query parameter",
			params: ManageAgento11yExperimentsReadParams{
				Operation: "list",
				agento11yExperimentFields: agento11yExperimentFields{
					SuiteID:   "suite-1",
					Status:    "completed",
					Source:    "external",
					CreatedBy: "runner@grafana.com",
					// The empty value must be dropped rather than sent, because an
					// empty tag= matches nothing upstream.
					Tag:    []string{"critical", "", "nightly"},
					From:   "2026-01-01T00:00:00Z",
					To:     "2026-01-08T00:00:00Z",
					Limit:  25,
					Cursor: "42",
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				// url.Values.Set would collapse these into one value and silently
				// narrow the filter to the last tag.
				assert.Equal(t, []string{"critical", "nightly"}, query["tag"])
				assert.Equal(t, "suite-1", query.Get("suite_id"))
				assert.Equal(t, "completed", query.Get("status"))
				assert.Equal(t, "external", query.Get("source"))
				assert.Equal(t, "runner@grafana.com", query.Get("created_by"))
				assert.Equal(t, "2026-01-01T00:00:00Z", query.Get("from"))
				assert.Equal(t, "2026-01-08T00:00:00Z", query.Get("to"))
				assert.Equal(t, "25", query.Get("limit"))
				assert.Equal(t, "42", query.Get("cursor"))

				writeJSONResponse(t, w, `{"items":[],"next_cursor":""}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yExperiment])
				require.True(t, ok)
				assert.Empty(t, resp.NextCursor, "the tool must not fetch another page on its own")
			},
		},
		{
			name: "list forwards a completed_at window and its order",
			params: ManageAgento11yExperimentsReadParams{
				Operation: "list",
				agento11yExperimentFields: agento11yExperimentFields{
					Order:         "completed_at_desc",
					CompletedFrom: "2026-01-01T00:00:00Z",
					CompletedTo:   "2026-01-08T00:00:00Z",
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				assert.Equal(t, "completed_at_desc", query.Get("order"))
				assert.Equal(t, "2026-01-01T00:00:00Z", query.Get("completed_from"))
				assert.Equal(t, "2026-01-08T00:00:00Z", query.Get("completed_to"))

				writeJSONResponse(t, w, `{"items":[],"next_cursor":""}`)
			},
		},
		{
			name:   "get escapes the experiment id",
			params: ManageAgento11yExperimentsReadParams{Operation: "get", ExperimentID: "exp/1 2"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiments/exp/1 2", r.URL.Path)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiments/exp%2F1%202", r.URL.EscapedPath())

				writeJSONResponse(t, w, `{"experiment_id":"exp/1 2","name":"n","status":"running"}`)
			},
			checkResult: func(t *testing.T, result any) {
				experiment, ok := result.(*Agento11yExperiment)
				require.True(t, ok)
				assert.Equal(t, "exp/1 2", experiment.ExperimentID)
			},
		},
		{
			name:   "list_trials paginates the experiment's trials",
			params: ManageAgento11yExperimentsReadParams{Operation: "list_trials", ExperimentID: "exp-1", agento11yExperimentFields: agento11yExperimentFields{Limit: 10, Cursor: "7"}},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiments/exp-1/trials", r.URL.Path)
				require.Equal(t, "10", r.URL.Query().Get("limit"))
				require.Equal(t, "7", r.URL.Query().Get("cursor"))

				writeJSONResponse(t, w, `{"items":[{"trial_id":"trial-1","experiment_id":"exp-1","test_case_id":"case-1","attempt":1,"status":"completed"}],"next_cursor":"9"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yTestCaseTrial])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "trial-1", resp.Items[0].TrialID)
				assert.Equal(t, "9", resp.NextCursor)
				// The /trials route runs no usage fallback, so an absent cost must
				// stay absent rather than decode as zero.
				assert.Nil(t, resp.Items[0].Cost)
				assert.Nil(t, resp.Items[0].TotalTokens)
			},
		},
		{
			name:   "list_scores reads the experiment's scores",
			params: ManageAgento11yExperimentsReadParams{Operation: "list_scores", ExperimentID: "exp-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiments/exp-1/scores", r.URL.Path)

				writeJSONResponse(t, w, `{"items":[{"score_id":"score-1","evaluator_id":"helpfulness","evaluator_version":"2","score_key":"final","score_type":"number","value":{"number":0.9},"passed":true,"trial_id":"trial-1"}],"next_cursor":""}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yEvalScore])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				score := resp.Items[0]
				assert.Equal(t, "trial-1", score.TrialID)
				require.NotNil(t, score.Value.Number)
				assert.InDelta(t, 0.9, *score.Value.Number, 1e-9)
				require.NotNil(t, score.Passed)
				assert.True(t, *score.Passed)
			},
		},
		{
			name:   "get_trial reads the top-level trial route",
			params: ManageAgento11yExperimentsReadParams{Operation: "get_trial", TrialID: "trial/1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-case-trials/trial%2F1", r.URL.EscapedPath())

				writeJSONResponse(t, w, `{"trial_id":"trial/1","experiment_id":"exp-1","test_case_id":"case-1","attempt":2,"status":"failed","test_case":{"test_case_id":"case-1","name":"capital of France","input":{"question":"?"},"expected":{"answer":"Paris"}}}`)
			},
			checkResult: func(t *testing.T, result any) {
				trial, ok := result.(*Agento11yTestCaseTrial)
				require.True(t, ok)
				assert.Equal(t, 2, trial.Attempt)
				// get_trial is the drill-down that get_report deliberately drops, so
				// the snapshot must survive here in full.
				require.NotNil(t, trial.TestCase)
				assert.Equal(t, map[string]any{"question": "?"}, trial.TestCase.Input)
				assert.Equal(t, map[string]any{"answer": "Paris"}, trial.TestCase.Expected)
			},
		},
		{
			name:   "list_trial_scores keeps the judge explanation",
			params: ManageAgento11yExperimentsReadParams{Operation: "list_trial_scores", TrialID: "trial-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-case-trials/trial-1/scores", r.URL.Path)

				writeJSONResponse(t, w, `{"items":[{"score_id":"score-1","evaluator_id":"helpfulness","evaluator_version":"2","score_key":"final","score_type":"number","value":{"number":0.2},"passed":false,"explanation":"the answer named the wrong city"}],"next_cursor":""}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yEvalScore])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "the answer named the wrong city", resp.Items[0].Explanation)
			},
		},
		{
			name:   "list_trial_artifacts returns metadata and a content ref",
			params: ManageAgento11yExperimentsReadParams{Operation: "list_trial_artifacts", TrialID: "trial-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-case-trials/trial-1/artifacts", r.URL.Path)

				writeJSONResponse(t, w, `{"items":[{"artifact_id":"art-1","parent_kind":"test_case_trial","parent_id":"trial-1","name":"screenshot.png","kind":"image","mime":"image/png","content_ref":"s3://bucket/art-1","size_bytes":2048}],"next_cursor":""}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yArtifact])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "s3://bucket/art-1", resp.Items[0].ContentRef)
				assert.Equal(t, int64(2048), resp.Items[0].SizeBytes)
			},
		},
		{
			name: "list_facets sends only the filters the route reads",
			params: ManageAgento11yExperimentsReadParams{
				Operation: "list_facets",
				agento11yExperimentFields: agento11yExperimentFields{
					Source: "collection",
					From:   "2026-01-01T00:00:00Z",
					To:     "2026-01-08T00:00:00Z",
					Limit:  10,
				},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiment-facets", r.URL.Path)
				query := r.URL.Query()
				assert.Equal(t, "collection", query.Get("source"))
				assert.Equal(t, "2026-01-01T00:00:00Z", query.Get("from"))
				assert.Equal(t, "2026-01-08T00:00:00Z", query.Get("to"))
				// The route has no pagination, so a limit is tolerated and dropped
				// rather than rejected.
				for _, ignored := range []string{"limit", "cursor"} {
					assert.False(t, query.Has(ignored), "%s is not read by the facets route", ignored)
				}

				writeJSONResponse(t, w, `{"owners":["runner@grafana.com"],"suites":["suite-1"],"tags":["critical","nightly"]}`)
			},
			checkResult: func(t *testing.T, result any) {
				facets, ok := result.(*Agento11yExperimentFacets)
				require.True(t, ok)
				assert.Equal(t, []string{"suite-1"}, facets.Suites)
				assert.Equal(t, []string{"critical", "nightly"}, facets.Tags)
			},
		},
		{
			name:    "unknown operation names only the read operations",
			params:  ManageAgento11yExperimentsReadParams{Operation: "cancel"},
			wantErr: `unknown operation "cancel", must be one of: ` + agento11yExperimentReadOperations,
		},
		{
			name:    "get requires an experiment id",
			params:  ManageAgento11yExperimentsReadParams{Operation: "get"},
			wantErr: `experiment_id is required for "get" operation`,
		},
		{
			name:    "get_report requires an experiment id",
			params:  ManageAgento11yExperimentsReadParams{Operation: "get_report"},
			wantErr: `experiment_id is required for "get_report" operation`,
		},
		{
			name:    "list_trials requires an experiment id",
			params:  ManageAgento11yExperimentsReadParams{Operation: "list_trials"},
			wantErr: `experiment_id is required for "list_trials" operation`,
		},
		{
			name:    "list_scores requires an experiment id",
			params:  ManageAgento11yExperimentsReadParams{Operation: "list_scores"},
			wantErr: `experiment_id is required for "list_scores" operation`,
		},
		{
			name:    "get_trial requires a trial id",
			params:  ManageAgento11yExperimentsReadParams{Operation: "get_trial"},
			wantErr: `trial_id is required for "get_trial" operation`,
		},
		{
			name:    "list_trial_scores requires a trial id",
			params:  ManageAgento11yExperimentsReadParams{Operation: "list_trial_scores"},
			wantErr: `trial_id is required for "list_trial_scores" operation`,
		},
		{
			name:    "list_trial_artifacts requires a trial id",
			params:  ManageAgento11yExperimentsReadParams{Operation: "list_trial_artifacts"},
			wantErr: `trial_id is required for "list_trial_artifacts" operation`,
		},
		{
			name: "completed_at ordering rejects a cursor",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{Order: "completed_at_desc", Cursor: "42"},
			},
			wantErr: "order 'completed_at_desc' cannot be combined with a cursor",
		},
		{
			name: "an unknown order is rejected",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{Order: "created_at_asc"},
			},
			wantErr: `unknown order "created_at_asc", must be one of: created_at_desc, completed_at_desc`,
		},
		{
			name: "an unknown status is rejected",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{Status: "succeeded"},
			},
			wantErr: `unknown status "succeeded", must be one of: running, completed, failed, canceled`,
		},
		{
			name: "an unknown source is rejected",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{Source: "manual"},
			},
			wantErr: `unknown source "manual", must be one of: collection, external`,
		},
		{
			name: "a reversed created_at window is rejected",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{From: "2026-01-08T00:00:00Z", To: "2026-01-01T00:00:00Z"},
			},
			wantErr: "from must not be after to",
		},
		{
			name: "a reversed completed_at window is rejected",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{CompletedFrom: "2026-01-08T00:00:00Z", CompletedTo: "2026-01-01T00:00:00Z"},
			},
			wantErr: "completed_from must not be after completed_to",
		},
		{
			name: "a reversed window is rejected on list_facets too",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list_facets",
				agento11yExperimentFields: agento11yExperimentFields{From: "2026-01-08T00:00:00Z", To: "2026-01-01T00:00:00Z"},
			},
			wantErr: "from must not be after to",
		},
		{
			// Dropping the filter would answer with every suite in the tenant while
			// looking like the answer for suite-1.
			name: "list_facets rejects a filter it would have to ignore",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list_facets",
				agento11yExperimentFields: agento11yExperimentFields{SuiteID: "suite-1"},
			},
			wantErr: "suite_id is not read by 'list_facets'",
		},
		{
			name: "list_facets rejects a tag filter it would have to ignore",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list_facets",
				agento11yExperimentFields: agento11yExperimentFields{Tag: []string{"critical"}},
			},
			wantErr: "tag is not read by 'list_facets'",
		},
		{
			// The trial statuses are the experiment status names, so a dropped
			// filter answers with every trial of the run while reading like the
			// running ones.
			name: "list_trials rejects a status filter it would drop",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list_trials",
				ExperimentID:              "exp-1",
				agento11yExperimentFields: agento11yExperimentFields{Status: "running"},
			},
			wantErr: `status is not accepted by "list_trials" operation, which would drop it: it is only read by 'list'`,
		},
		{
			name: "list_scores rejects a suite filter it would drop",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list_scores",
				ExperimentID:              "exp-1",
				agento11yExperimentFields: agento11yExperimentFields{SuiteID: "suite-1"},
			},
			wantErr: `suite_id is not accepted by "list_scores" operation, which would drop it: it is only read by 'list'`,
		},
		{
			name: "get_trial rejects a tag filter it would drop",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "get_trial",
				TrialID:                   "trial-1",
				agento11yExperimentFields: agento11yExperimentFields{Tag: []string{"critical"}},
			},
			wantErr: `tag is not accepted by "get_trial" operation, which would drop it: it is only read by 'list'`,
		},
		{
			// row_limit trims the report rows; on a paginated route it reads like
			// limit and would leave the page at its default size.
			name: "list_trials rejects the report row limit",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list_trials",
				ExperimentID:              "exp-1",
				agento11yExperimentFields: agento11yExperimentFields{RowLimit: 100},
			},
			wantErr: `row_limit is not accepted by "list_trials" operation, which would drop it: it is only read by 'get_report'`,
		},
		{
			name: "get_report rejects a source filter it would drop",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "get_report",
				ExperimentID:              "exp-1",
				agento11yExperimentFields: agento11yExperimentFields{Source: "collection"},
			},
			wantErr: `source is not accepted by "get_report" operation, which would drop it: it is only read by 'list', 'list_facets'`,
		},
		{
			// The bound re-resolves on the next call, moving the window the cursor
			// was issued against.
			name: "a cursor is rejected alongside a relative bound",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{Cursor: "42", From: "now-7d"},
			},
			wantErr: `from="now-7d" is relative and re-resolves between calls`,
		},
		{
			name: "a cursor is accepted alongside an absolute bound",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{Cursor: "42", From: "2026-01-01T00:00:00Z"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "42", r.URL.Query().Get("cursor"))
				writeJSONResponse(t, w, `{"items":[],"next_cursor":""}`)
			},
		},
		{
			name: "a relative bound is accepted on a first page",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{From: "now-7d"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				assert.True(t, r.URL.Query().Has("from"), "a relative bound is resolved and sent")
				writeJSONResponse(t, w, `{"items":[],"next_cursor":""}`)
			},
		},
		{
			name: "an unparsable time bound is rejected",
			params: ManageAgento11yExperimentsReadParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{From: "last tuesday"},
			},
			wantErr: "parsing from",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				if tc.handler == nil {
					t.Error("server should not be called for validation failures")
					return
				}
				tc.handler(t, w, r)
			})
			defer server.Close()

			result, err := manageAgento11yExperimentsRead(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.checkResult != nil {
				tc.checkResult(t, result)
			}
		})
	}
}

func TestAgento11yManageExperimentsReadWrite(t *testing.T) {
	name := "renamed run"
	blank := "  "
	description := "triaged after the incident"
	emptyDescription := ""
	tags := []string{"triaged"}
	noTags := []string{}

	testCases := []struct {
		name        string
		params      ManageAgento11yExperimentsReadWriteParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "the read operations are reachable from the write variant",
			params: ManageAgento11yExperimentsReadWriteParams{Operation: "get", ExperimentID: "exp-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiments/exp-1", r.URL.Path)

				writeJSONResponse(t, w, `{"experiment_id":"exp-1","name":"nightly","status":"completed"}`)
			},
			checkResult: func(t *testing.T, result any) {
				experiment, ok := result.(*Agento11yExperiment)
				require.True(t, ok)
				assert.Equal(t, "exp-1", experiment.ExperimentID)
			},
		},
		{
			name: "update sends only the supplied fields",
			params: ManageAgento11yExperimentsReadWriteParams{
				Operation:    "update",
				ExperimentID: "exp-1",
				Name:         &name,
				Tags:         &tags,
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPatch, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiments/exp-1", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "renamed run", body["name"])
				assert.Equal(t, []any{"triaged"}, body["tags"])
				// An absent field is left unchanged upstream, so sending it as a
				// zero value would clear it.
				assert.NotContains(t, body, "description")
				assert.NotContains(t, body, "metadata")
				// Status is not patchable: the API answers 409 and points at cancel.
				assert.NotContains(t, body, "status")
				writeJSONResponse(t, w, `{"experiment_id":"exp-1","name":"renamed run","status":"running","tags":["triaged"]}`)
			},
			checkResult: func(t *testing.T, result any) {
				experiment, ok := result.(*Agento11yExperiment)
				require.True(t, ok)
				assert.Equal(t, "renamed run", experiment.Name)
			},
		},
		{
			name: "update sends a description and a metadata object",
			params: ManageAgento11yExperimentsReadWriteParams{
				Operation:    "update",
				ExperimentID: "exp-1",
				Description:  &description,
				Metadata:     map[string]any{"triage_owner": "alice"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				assert.Equal(t, "triaged after the incident", body["description"])
				assert.Equal(t, map[string]any{"triage_owner": "alice"}, body["metadata"])
				assert.NotContains(t, body, "name")
				assert.NotContains(t, body, "tags")

				writeJSONResponse(t, w, `{"experiment_id":"exp-1","name":"nightly","status":"running"}`)
			},
		},
		{
			name: "update clears a description and a tag list explicitly",
			params: ManageAgento11yExperimentsReadWriteParams{
				Operation:    "update",
				ExperimentID: "exp-1",
				Description:  &emptyDescription,
				Tags:         &noTags,
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				assert.Equal(t, "", body["description"])
				assert.Equal(t, []any{}, body["tags"])

				writeJSONResponse(t, w, `{"experiment_id":"exp-1","name":"nightly","status":"running"}`)
			},
		},
		{
			name:   "cancel appends the action after the id is escaped",
			params: ManageAgento11yExperimentsReadWriteParams{Operation: "cancel", ExperimentID: "exp/1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				// The plugin proxy forwards a POST only when the last path segment
				// ends in ":cancel", so the colon must stay unescaped while the ID
				// around it is escaped.
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiments/exp%2F1:cancel", r.URL.EscapedPath())

				writeJSONResponse(t, w, `{"experiment_id":"exp/1","name":"nightly","status":"canceled"}`)
			},
			checkResult: func(t *testing.T, result any) {
				experiment, ok := result.(*Agento11yExperiment)
				require.True(t, ok)
				assert.Equal(t, "canceled", experiment.Status)
			},
		},
		{
			// Upstream flips the status only while the run is still running. A run
			// that already finished is left alone and the call still succeeds, so
			// the returned status is the only thing that says nothing was stopped.
			name:   "cancel of a finished run returns it unchanged",
			params: ManageAgento11yExperimentsReadWriteParams{Operation: "cancel", ExperimentID: "exp-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				writeJSONResponse(t, w, `{"experiment_id":"exp-1","name":"nightly","status":"failed"}`)
			},
			checkResult: func(t *testing.T, result any) {
				experiment, ok := result.(*Agento11yExperiment)
				require.True(t, ok)
				assert.Equal(t, "failed", experiment.Status)
			},
		},
		{
			name:    "update requires an experiment id",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "update", Name: &name},
			wantErr: "experiment_id is required for 'update' operation",
		},
		{
			name:    "update requires at least one field",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "update", ExperimentID: "exp-1"},
			wantErr: "at least one of name, description, tags, or metadata is required",
		},
		{
			name:   "metadata alone satisfies the update field requirement",
			params: ManageAgento11yExperimentsReadWriteParams{Operation: "update", ExperimentID: "exp-1", Metadata: map[string]any{"triaged": true}},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, map[string]any{"triaged": true}, decodeRequestBody(t, r)["metadata"])
				writeJSONResponse(t, w, `{"experiment_id":"exp-1","name":"nightly","status":"running"}`)
			},
		},
		{
			name:    "update rejects a blank name",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "update", ExperimentID: "exp-1", Name: &blank},
			wantErr: "name must not be blank for 'update' operation",
		},
		{
			name:    "cancel requires an experiment id",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "cancel"},
			wantErr: "experiment_id is required for 'cancel' operation",
		},
		{
			// Cancel sends no body, so a patch set alongside it would stop the
			// experiment and report success having discarded the patch.
			name:    "cancel rejects an update tag list rather than dropping it",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "cancel", ExperimentID: "exp-1", Tags: &tags},
			wantErr: `tags is not accepted by 'cancel' operation, which would drop it: it is only read by 'update'`,
		},
		{
			name:    "cancel rejects an update name rather than dropping it",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "cancel", ExperimentID: "exp-1", Name: &name},
			wantErr: `name is not accepted by 'cancel' operation, which would drop it: it is only read by 'update'`,
		},
		{
			name:    "cancel rejects update metadata rather than dropping it",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "cancel", ExperimentID: "exp-1", Metadata: map[string]any{"triaged": true}},
			wantErr: `metadata is not accepted by 'cancel' operation, which would drop it: it is only read by 'update'`,
		},
		{
			// Cancel stops the whole experiment, so a trial ID alongside it would
			// cancel every trial of the run while reading as trial-scoped.
			name:    "cancel rejects a trial id rather than dropping it",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "cancel", ExperimentID: "exp-1", TrialID: "trial-1"},
			wantErr: `trial_id is not accepted by "cancel" operation, which would drop it: it is only read by 'get_trial', 'list_trial_scores', 'list_trial_artifacts'`,
		},
		{
			name:    "update rejects a trial id rather than dropping it",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "update", ExperimentID: "exp-1", TrialID: "trial-1", Name: &name},
			wantErr: `trial_id is not accepted by "update" operation, which would drop it: it is only read by 'get_trial', 'list_trial_scores', 'list_trial_artifacts'`,
		},
		{
			name:    "unknown operation names every operation the variant has",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "create"},
			wantErr: `unknown operation "create", must be one of: ` + agento11yExperimentAllOperations,
		},
		{
			name: "list validation still applies on the write variant",
			params: ManageAgento11yExperimentsReadWriteParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{Order: "completed_at_desc", Cursor: "42"},
			},
			wantErr: "order 'completed_at_desc' cannot be combined with a cursor",
		},
		{
			// 'tags' is the update field and 'tag' is the filter. Accepting 'tags'
			// on 'list' and dropping it would answer across the whole tenant.
			name:    "list rejects the update tag list rather than dropping it",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "list", Tags: &tags},
			wantErr: "tags is only read by the 'update' operation, not by \"list\"; the tag filter for the read operations is the separate 'tag' parameter",
		},
		{
			// rejectFacetFilters guards 'tag' and structurally cannot see 'tags'.
			name:    "list_facets rejects the update tag list too",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "list_facets", Tags: &tags},
			wantErr: `tags is only read by the 'update' operation, not by "list_facets"`,
		},
		{
			name:    "a read operation rejects the update name",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "get", ExperimentID: "exp-1", Name: &name},
			wantErr: `name is only read by the 'update' operation, not by "get"`,
		},
		{
			name:    "a read operation rejects the update description",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "get", ExperimentID: "exp-1", Description: &description},
			wantErr: `description is only read by the 'update' operation, not by "get"`,
		},
		{
			name:    "a read operation rejects the update metadata",
			params:  ManageAgento11yExperimentsReadWriteParams{Operation: "list_trials", ExperimentID: "exp-1", Metadata: map[string]any{"triaged": true}},
			wantErr: `metadata is only read by the 'update' operation, not by "list_trials"`,
		},
		{
			// The 'tag' filter is not an update field, so it still reaches the wire.
			name: "the tag filter still reaches the query on the write variant",
			params: ManageAgento11yExperimentsReadWriteParams{
				Operation:                 "list",
				agento11yExperimentFields: agento11yExperimentFields{Tag: []string{"critical"}},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, []string{"critical"}, r.URL.Query()["tag"])
				writeJSONResponse(t, w, `{"items":[],"next_cursor":""}`)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				if tc.handler == nil {
					t.Error("server should not be called for validation failures")
					return
				}
				tc.handler(t, w, r)
			})
			defer server.Close()

			result, err := manageAgento11yExperimentsReadWrite(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.checkResult != nil {
				tc.checkResult(t, result)
			}
		})
	}
}

// TestAgento11yManageExperimentsGetReport covers the reshaping get_report does,
// the only response this tool does not pass through.
func TestAgento11yManageExperimentsGetReport(t *testing.T) {
	// One row with everything get_report is expected to drop.
	fullRow := `{
		"test_case_id": "case-1",
		"test_case_snapshot": {
			"test_case_id": "case-1",
			"name": "capital of France",
			"category": "geography",
			"tags": ["smoke"],
			"input": {"question": "what is the capital of France"},
			"expected": {"answer": "Paris"},
			"metadata": {"owner": "triage"}
		},
		"summary": {"trial_count": 2, "completed_count": 2, "trial_pass_rate": 0.5, "pass_at_k": {"1": true}},
		"trials": [
			{
				"trial": {
					"trial_id": "trial-1", "experiment_id": "exp-1", "test_case_id": "case-1",
					"attempt": 1, "status": "completed", "conversation_id": "conv-1", "trace_id": "trace-1",
					"cost": 0.014, "duration_ms": 4200, "total_tokens": 1900, "input_tokens": 1200, "output_tokens": 700,
					"test_case": {"test_case_id": "case-1", "input": {"question": "what is the capital of France"}}
				},
				"final_score": {"score_id":"s-final","evaluator_id":"helpfulness","evaluator_version":"2","score_key":"final","score_type":"number","value":{"number":0.9},"passed":true,"explanation":"a long judge rationale"},
				"scores": [
					{"score_id":"s-final","evaluator_id":"helpfulness","evaluator_version":"2","score_key":"final","score_type":"number","value":{"number":0.9},"passed":true,"explanation":"a long judge rationale"},
					{"score_id":"s-latency","evaluator_id":"latency","evaluator_version":"1","score_key":"latency_ms","score_type":"number","value":{"number":4200}}
				],
				"artifacts": [
					{"artifact_id":"art-1","parent_kind":"test_case_trial","parent_id":"trial-1","name":"a.png","kind":"image","content_ref":"s3://bucket/art-1"},
					{"artifact_id":"art-2","parent_kind":"test_case_trial","parent_id":"trial-1","name":"b.json","kind":"json","content_ref":"s3://bucket/art-2"}
				]
			}
		]
	}`

	t.Run("a full row is reduced to the fields with no size bound removed", func(t *testing.T) {
		server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/experiments/exp-1/report", r.URL.Path)
			// The route is not paginated upstream and reads no query parameters.
			require.Empty(t, r.URL.RawQuery)

			writeJSONResponse(t, w, `{
				"experiment": {"experiment_id":"exp-1","name":"nightly","status":"completed"},
				"summary": {"test_case_count":1,"trial_count":2,"completed_count":2,"pass_rate":0.5,"total_tokens":3800,"token_coverage":"complete"},
				"rows": [`+fullRow+`]
			}`)
		})
		defer server.Close()

		result, err := manageAgento11yExperimentsRead(ctx, ManageAgento11yExperimentsReadParams{Operation: "get_report", ExperimentID: "exp-1"})
		require.NoError(t, err)

		report, ok := result.(*Agento11yCompactExperimentReport)
		require.True(t, ok)
		assert.Equal(t, "exp-1", report.Experiment.ExperimentID)
		require.NotNil(t, report.Summary.PassRate)
		assert.InDelta(t, 0.5, *report.Summary.PassRate, 1e-9)
		assert.Equal(t, 1, report.TotalRowCount)
		assert.False(t, report.RowsTruncated)

		require.Len(t, report.Rows, 1)
		row := report.Rows[0]
		assert.Equal(t, "case-1", row.TestCaseID)
		// The identifying snapshot fields are lifted onto the row; the free-form
		// ones are not.
		assert.Equal(t, "capital of France", row.Name)
		assert.Equal(t, "geography", row.Category)
		assert.Equal(t, []string{"smoke"}, row.Tags)
		assert.Equal(t, 2, row.Summary.TrialCount)
		assert.Equal(t, map[string]bool{"1": true}, row.Summary.PassAtK)

		require.Len(t, row.Trials, 1)
		trial := row.Trials[0]
		assert.Equal(t, "trial-1", trial.TrialID)
		assert.Equal(t, 1, trial.Attempt)
		assert.Equal(t, "completed", trial.Status)
		assert.Equal(t, "conv-1", trial.ConversationID)
		assert.Equal(t, "trace-1", trial.TraceID)
		require.NotNil(t, trial.Cost)
		assert.InDelta(t, 0.014, *trial.Cost, 1e-9)
		require.NotNil(t, trial.DurationMS)
		assert.Equal(t, int64(4200), *trial.DurationMS)
		require.NotNil(t, trial.TotalTokens)
		assert.Equal(t, int64(1900), *trial.TotalTokens)
		// The score and artifact records collapse to their counts;
		// list_trial_scores and list_trial_artifacts return the records.
		assert.Equal(t, 2, trial.ScoreCount)
		assert.Equal(t, 2, trial.ArtifactCount)

		require.NotNil(t, trial.FinalScore)
		assert.Equal(t, "final", trial.FinalScore.ScoreKey)
		assert.Equal(t, "helpfulness", trial.FinalScore.EvaluatorID)
		require.NotNil(t, trial.FinalScore.Value.Number)
		assert.InDelta(t, 0.9, *trial.FinalScore.Value.Number, 1e-9)
		require.NotNil(t, trial.FinalScore.Passed)
		assert.True(t, *trial.FinalScore.Passed)

		// The dropped payloads are checked on the serialized form, because that
		// is what a model receives.
		encoded, err := json.Marshal(report)
		require.NoError(t, err)
		for _, dropped := range []string{
			"what is the capital of France", // test_case_snapshot.input and the per-trial snapshot
			"Paris",                         // test_case_snapshot.expected
			"a long judge rationale",        // the explanation on the final score
			"latency_ms",                    // a non-final score
			"s3://bucket/art-1",             // a full artifact record
		} {
			assert.NotContains(t, string(encoded), dropped, "get_report must not return %q", dropped)
		}
	})

	// Without the error field a failed row reads as "status: failed" and nothing
	// else, and the reason costs a get_trial that pulls back the snapshot that
	// compaction removed.
	t.Run("a failed trial keeps the reason it failed", func(t *testing.T) {
		server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
			writeJSONResponse(t, w, `{
				"experiment": {"experiment_id":"exp-1","name":"nightly","status":"completed"},
				"summary": {"test_case_count":1,"trial_count":1,"failed_count":1},
				"rows": [{
					"test_case_id": "case-1",
					"summary": {"trial_count": 1},
					"trials": [{
						"trial": {"trial_id":"trial-1","attempt":1,"status":"failed","error":"the agent timed out after 60s"},
						"scores": [],
						"artifacts": []
					}]
				}]
			}`)
		})
		defer server.Close()

		result, err := manageAgento11yExperimentsRead(ctx, ManageAgento11yExperimentsReadParams{Operation: "get_report", ExperimentID: "exp-1"})
		require.NoError(t, err)

		report, ok := result.(*Agento11yCompactExperimentReport)
		require.True(t, ok)
		require.Len(t, report.Rows, 1)
		require.Len(t, report.Rows[0].Trials, 1)
		assert.Equal(t, "the agent timed out after 60s", report.Rows[0].Trials[0].Error)
	})

	for _, tc := range []struct {
		name          string
		rowLimit      int
		totalRows     int
		wantRows      int
		wantTruncated bool
	}{
		{name: "the default row limit is 50", rowLimit: 0, totalRows: 75, wantRows: 50, wantTruncated: true},
		{name: "an explicit row limit is honoured", rowLimit: 10, totalRows: 75, wantRows: 10, wantTruncated: true},
		{name: "a report under the limit is not truncated", rowLimit: 50, totalRows: 12, wantRows: 12, wantTruncated: false},
		{name: "a report exactly at the limit is not truncated", rowLimit: 50, totalRows: 50, wantRows: 50, wantTruncated: false},
		{name: "a negative row limit falls back to the default", rowLimit: -5, totalRows: 75, wantRows: 50, wantTruncated: true},
		{name: "the row limit is capped at 500", rowLimit: 5000, totalRows: 600, wantRows: 500, wantTruncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				rows := make([]map[string]any, 0, tc.totalRows)
				for i := range tc.totalRows {
					rows = append(rows, map[string]any{
						"test_case_id": "case-" + strconv.Itoa(i),
						"summary":      map[string]any{"trial_count": 1},
						"trials":       []any{},
					})
				}
				body, err := json.Marshal(map[string]any{
					"experiment": map[string]any{"experiment_id": "exp-1"},
					"summary":    map[string]any{"test_case_count": tc.totalRows},
					"rows":       rows,
				})
				require.NoError(t, err)
				writeJSONResponse(t, w, string(body))
			})
			defer server.Close()

			result, err := manageAgento11yExperimentsRead(ctx, ManageAgento11yExperimentsReadParams{
				Operation:                 "get_report",
				ExperimentID:              "exp-1",
				agento11yExperimentFields: agento11yExperimentFields{RowLimit: tc.rowLimit},
			})
			require.NoError(t, err)

			report, ok := result.(*Agento11yCompactExperimentReport)
			require.True(t, ok)
			assert.Len(t, report.Rows, tc.wantRows)
			assert.Equal(t, tc.totalRows, report.TotalRowCount)
			assert.Equal(t, tc.wantTruncated, report.RowsTruncated)
		})
	}
}

// TestAgento11yManageExperimentsToolContract pins the parts of the advertised
// tool a client relies on but no request test touches: the annotations that
// tell a caller whether it is safe to run, and the guidance that keeps it from
// fetching an unbounded report or paginating with a window that moves.
func TestAgento11yManageExperimentsToolContract(t *testing.T) {
	read := ManageAgento11yExperimentsRead.Tool
	require.NotNil(t, read.Annotations.ReadOnlyHint)
	assert.True(t, read.Annotations.ReadOnlyHint)
	require.NotNil(t, read.Annotations.IdempotentHint)
	assert.True(t, read.Annotations.IdempotentHint)
	require.NotNil(t, read.Annotations.DestructiveHint)
	assert.False(t, *read.Annotations.DestructiveHint)

	write := ManageAgento11yExperimentsReadWrite.Tool
	require.NotNil(t, write.Annotations.ReadOnlyHint)
	assert.False(t, write.Annotations.ReadOnlyHint, "the write variant cancels experiments")
	require.NotNil(t, write.Annotations.DestructiveHint)
	assert.True(t, *write.Annotations.DestructiveHint)

	for _, guidance := range []string{
		// The report is fetched whole, so a caller that does not know the ceiling
		// reads a failed call as a missing experiment.
		"above 10 MiB fails the call",
		// The cursor is bound to the window it was issued with.
		"absolute RFC3339 times",
		// A 403 is unreadable without the role that causes it.
		"grafana-agento11y-app.data:read",
	} {
		assert.Contains(t, read.Description, guidance)
	}
	assert.Contains(t, write.Description, "grafana-agento11y-app.eval:write")
}

// TestAgento11yExperimentRoutesAvoidDeadEndpoints pins the proxied routes this
// tool deliberately does not call. /eval/experiment-summaries is forwarded by
// the plugin proxy but no longer exists upstream, and the creation and
// finalization routes answer 405 at the proxy.
func TestAgento11yExperimentRoutesAvoidDeadEndpoints(t *testing.T) {
	for _, params := range []ManageAgento11yExperimentsReadWriteParams{
		{Operation: "list"},
		{Operation: "get", ExperimentID: "exp-1"},
		{Operation: "get_report", ExperimentID: "exp-1"},
		{Operation: "list_trials", ExperimentID: "exp-1"},
		{Operation: "list_scores", ExperimentID: "exp-1"},
		{Operation: "list_facets"},
		{Operation: "update", ExperimentID: "exp-1", Metadata: map[string]any{"triaged": true}},
		{Operation: "cancel", ExperimentID: "exp-1"},
	} {
		t.Run(params.Operation, func(t *testing.T) {
			var requested []string
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				requested = append(requested, r.Method+" "+r.URL.Path)
				writeJSONResponse(t, w, `{"experiment_id":"exp-1","items":[],"next_cursor":"","rows":[]}`)
			})
			defer server.Close()

			_, err := manageAgento11yExperimentsReadWrite(ctx, params)
			require.NoError(t, err)
			require.Len(t, requested, 1, "each operation makes exactly one request")

			const base = "/api/plugins/grafana-agento11y-app/resources"
			for _, dead := range []string{
				"GET " + base + "/eval/experiment-summaries",
				"POST " + base + "/eval/experiments",
				"POST " + base + "/eval/experiments/exp-1:finalize",
				"POST " + base + "/eval/experiments/exp-1/trials",
			} {
				assert.NotEqual(t, dead, requested[0], "%s has no working handler behind the plugin proxy", dead)
			}
		})
	}
}

// TestAgento11yExperimentDecoding checks the fields this tool depends on
// against the sparse bodies the API actually sends, where an aggregate it could
// not compute is omitted rather than sent as zero.
func TestAgento11yExperimentDecoding(t *testing.T) {
	t.Run("omitted aggregates stay nil rather than becoming zero", func(t *testing.T) {
		var experiment Agento11yExperiment
		require.NoError(t, json.Unmarshal([]byte(`{
			"experiment_id": "exp-1",
			"name": "nightly",
			"status": "running",
			"result": {"test_case_count": 3, "trial_count": 0, "completed_count": 0, "failed_count": 0, "canceled_count": 0}
		}`), &experiment))

		assert.Equal(t, "running", experiment.Status)
		assert.Nil(t, experiment.PlannedTrialCount, "an unknown plan size is not a planned zero")
		assert.Nil(t, experiment.StartedAt)
		assert.Nil(t, experiment.CompletedAt)
		require.NotNil(t, experiment.Result)
		assert.Nil(t, experiment.Result.PassRate, "no pass rate yet is not a 0% pass rate")
		assert.Nil(t, experiment.Result.TotalCost)
		assert.Nil(t, experiment.Result.TotalTokens)
		assert.Nil(t, experiment.Result.FinalScoreAvg)
	})

	t.Run("a zero pass rate is distinct from an absent one", func(t *testing.T) {
		var summary Agento11yExperimentReportSummary
		require.NoError(t, json.Unmarshal([]byte(`{"pass_rate": 0, "total_tokens": 0, "total_cost": 0}`), &summary))

		require.NotNil(t, summary.PassRate)
		assert.Zero(t, *summary.PassRate)
		require.NotNil(t, summary.TotalTokens)
		assert.Zero(t, *summary.TotalTokens)
		require.NotNil(t, summary.TotalCost)
		assert.Zero(t, *summary.TotalCost)
	})

	// The shape the Agent Observability SDK pins in
	// conformance/experiments/responses.json under report_experiment_envelope:
	// a report whose rows carry no snapshot, no row summary, and no artifacts.
	t.Run("a sparse report envelope decodes and compacts", func(t *testing.T) {
		var report Agento11yExperimentReport
		require.NoError(t, json.Unmarshal([]byte(`{
			"experiment": {"experiment_id": "conformance-run", "name": "conformance run", "status": "completed"},
			"summary": {"test_case_count": 1, "trial_count": 1, "completed_count": 1, "failed_count": 0, "canceled_count": 0},
			"rows": [{
				"test_case_id": "capital-france",
				"trials": [{
					"trial": {"trial_id": "trial-e82b5e46a40a37e5", "status": "completed"},
					"scores": [{"score_key": "helpfulness", "evaluator_id": "helpfulness"}]
				}]
			}]
		}`), &report))

		require.Len(t, report.Rows, 1)
		assert.Nil(t, report.Rows[0].TestCaseSnapshot)

		compact := compactAgento11yReport(report, 0)
		require.Len(t, compact.Rows, 1)
		assert.Equal(t, "capital-france", compact.Rows[0].TestCaseID)
		assert.Empty(t, compact.Rows[0].Name, "a row with no snapshot carries no name")
		require.Len(t, compact.Rows[0].Trials, 1)
		assert.Equal(t, "trial-e82b5e46a40a37e5", compact.Rows[0].Trials[0].TrialID)
		assert.Nil(t, compact.Rows[0].Trials[0].FinalScore, "no score is keyed final in this envelope")
		// A trial with no final score can still have been scored, which is what
		// separates "not scored" from "scored, no verdict".
		assert.Equal(t, 1, compact.Rows[0].Trials[0].ScoreCount)
		assert.Zero(t, compact.Rows[0].Trials[0].ArtifactCount)
		assert.False(t, compact.RowsTruncated)
	})

	// Marshalling an experiment would not catch these: every field carries
	// omitempty or omitzero, so declaring one and leaving it unset produces the
	// same JSON as not declaring it at all.
	t.Run("the four fields the API marks json:- are not declared on the wire type", func(t *testing.T) {
		declared := map[string]bool{}
		for _, field := range reflect.VisibleFields(reflect.TypeOf(Agento11yExperiment{})) {
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			declared[name] = true
		}
		for _, absent := range []string{"source", "collection_id", "evaluators", "score_count"} {
			assert.False(t, declared[absent], "the API never sends %q, so declaring it would advertise a field that is always empty", absent)
		}
	})
}
