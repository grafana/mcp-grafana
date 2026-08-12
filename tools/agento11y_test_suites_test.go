//go:build unit
// +build unit

package tools

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgento11yManageTestSuitesRead(t *testing.T) {
	testCases := []struct {
		name        string
		params      ManageAgento11yTestSuitesReadParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "list_suites sends the default limit",
			params: ManageAgento11yTestSuitesReadParams{Operation: "list_suites"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
				require.Equal(t, "50", r.URL.Query().Get("limit"))
				require.False(t, r.URL.Query().Has("cursor"))

				writeJSONResponse(t, w, `{"items":[{
					"tenant_id": "tenant-1",
					"suite_id": "suite-1",
					"name": "triage regression",
					"description": "cases the triage agent must not regress on",
					"tags": ["critical"],
					"latest_version": "v2",
					"created_by": "alice@grafana.com",
					"created_at": "2026-01-02T03:04:05Z"
				}],"next_cursor":"42"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yTestSuite])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "42", resp.NextCursor)

				suite := resp.Items[0]
				assert.Equal(t, "suite-1", suite.SuiteID)
				assert.Equal(t, "triage regression", suite.Name)
				assert.Equal(t, []string{"critical"}, suite.Tags)
				assert.Equal(t, "v2", suite.LatestVersion)
				// Only the single-suite route carries the version history.
				assert.Empty(t, suite.Versions)
			},
		},
		{
			name:   "list_suites forwards an explicit page",
			params: ManageAgento11yTestSuitesReadParams{Operation: "list_suites", agento11yTestSuiteFields: agento11yTestSuiteFields{Limit: 10, Cursor: "42"}},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "10", r.URL.Query().Get("limit"))
				require.Equal(t, "42", r.URL.Query().Get("cursor"))

				writeJSONResponse(t, w, `{"items":[],"next_cursor":""}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yTestSuite])
				require.True(t, ok)
				assert.Empty(t, resp.NextCursor, "the tool must not fetch another page on its own")
			},
		},
		{
			name:   "get_suite carries the version history and escapes the id",
			params: ManageAgento11yTestSuitesReadParams{Operation: "get_suite", SuiteID: "suite/1 2"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite%2F1%202", r.URL.EscapedPath())

				writeJSONResponse(t, w, `{
					"suite_id": "suite/1 2",
					"name": "triage regression",
					"latest_version": "v1",
					"versions": [
						{"suite_id":"suite/1 2","version":"v1","test_case_count":12,"published":true,"changelog":"first cut","published_by":"alice@grafana.com","published_at":"2026-01-03T00:00:00Z","created_at":"2026-01-02T00:00:00Z"},
						{"suite_id":"suite/1 2","version":"v2","test_case_count":13,"published":false,"source_version":"v1","created_at":"2026-01-04T00:00:00Z"}
					]
				}`)
			},
			checkResult: func(t *testing.T, result any) {
				suite, ok := result.(*Agento11yTestSuite)
				require.True(t, ok)
				assert.Equal(t, "suite/1 2", suite.SuiteID)
				require.Len(t, suite.Versions, 2)

				published := suite.Versions[0]
				assert.Equal(t, "v1", published.Version)
				assert.True(t, published.Published)
				assert.Equal(t, 12, published.TestCaseCount)
				require.NotNil(t, published.PublishedAt)

				draft := suite.Versions[1]
				assert.Equal(t, "v2", draft.Version)
				assert.False(t, draft.Published)
				assert.Equal(t, "v1", draft.SourceVersion)
				assert.Nil(t, draft.PublishedAt, "a draft has no publish time, and an absent one must not decode as the zero instant")
			},
		},
		{
			name:   "list_test_cases addresses a case through its suite version",
			params: ManageAgento11yTestSuitesReadParams{Operation: "list_test_cases", SuiteID: "suite-1", Version: "v2", agento11yTestSuiteFields: agento11yTestSuiteFields{Limit: 5}},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite-1/versions/v2/test-cases", r.URL.Path)
				require.Equal(t, "5", r.URL.Query().Get("limit"))

				writeJSONResponse(t, w, `{"items":[{"test_case_id":"case-3","suite_id":"suite-1","suite_version":"v2","name":"capital of France","category":"geography","tags":["smoke"],"input":{"question":"what is the capital of France"},"expected":{"answer":"Paris"}}],"next_cursor":""}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yTestCase])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				testCase := resp.Items[0]
				assert.Equal(t, "case-3", testCase.TestCaseID)
				assert.Equal(t, "v2", testCase.SuiteVersion)
				assert.Equal(t, map[string]any{"question": "what is the capital of France"}, testCase.Input)
				assert.Equal(t, map[string]any{"answer": "Paris"}, testCase.Expected)
			},
		},
		{
			name:   "get_test_case escapes every path segment",
			params: ManageAgento11yTestSuitesReadParams{Operation: "get_test_case", SuiteID: "suite/1", Version: "v 2", TestCaseID: "case/3"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite%2F1/versions/v%202/test-cases/case%2F3", r.URL.EscapedPath())

				writeJSONResponse(t, w, `{"test_case_id":"case/3","input":{"question":"?"},"artifact_refs":[{"artifact_id":"art-1","name":"a.png","kind":"image"}]}`)
			},
			checkResult: func(t *testing.T, result any) {
				testCase, ok := result.(*Agento11yTestCase)
				require.True(t, ok)
				assert.Equal(t, "case/3", testCase.TestCaseID)
				require.Len(t, testCase.ArtifactRefs, 1)
				assert.Equal(t, "art-1", testCase.ArtifactRefs[0].ArtifactID)
			},
		},
		{
			name:    "unknown operation names only the read operations",
			params:  ManageAgento11yTestSuitesReadParams{Operation: "create_suite"},
			wantErr: `unknown operation "create_suite", must be one of: ` + agento11yTestSuiteReadOperations,
		},
		{
			// Version history comes from get_suite. The standalone version route is
			// forwarded by the plugin proxy but answers 400 upstream.
			name:    "reading one version on its own is not an operation",
			params:  ManageAgento11yTestSuitesReadParams{Operation: "get_version", SuiteID: "suite-1", Version: "v2"},
			wantErr: `unknown operation "get_version"`,
		},
		{
			name:    "listing versions is not an operation either",
			params:  ManageAgento11yTestSuitesReadParams{Operation: "list_versions", SuiteID: "suite-1"},
			wantErr: `unknown operation "list_versions"`,
		},
		{
			name:    "get_suite requires a suite id",
			params:  ManageAgento11yTestSuitesReadParams{Operation: "get_suite"},
			wantErr: `suite_id is required for "get_suite" operation`,
		},
		{
			name:    "list_test_cases requires a suite id",
			params:  ManageAgento11yTestSuitesReadParams{Operation: "list_test_cases", Version: "v2"},
			wantErr: `suite_id is required for "list_test_cases" operation`,
		},
		{
			name:    "list_test_cases requires a version",
			params:  ManageAgento11yTestSuitesReadParams{Operation: "list_test_cases", SuiteID: "suite-1"},
			wantErr: `version is required for "list_test_cases" operation`,
		},
		{
			name:    "get_test_case requires a version",
			params:  ManageAgento11yTestSuitesReadParams{Operation: "get_test_case", SuiteID: "suite-1", TestCaseID: "case-3"},
			wantErr: `version is required for "get_test_case" operation`,
		},
		{
			name:    "get_test_case requires a test case id",
			params:  ManageAgento11yTestSuitesReadParams{Operation: "get_test_case", SuiteID: "suite-1", Version: "v2"},
			wantErr: `test_case_id is required for "get_test_case" operation`,
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

			result, err := manageAgento11yTestSuitesRead(ctx, tc.params)
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

func TestAgento11yManageTestSuitesReadWrite(t *testing.T) {
	name := "triage regression"
	blank := "  "
	description := "cases the triage agent must not regress on"
	emptyDescription := ""
	tags := []string{"critical"}
	noTags := []string{}

	testCases := []struct {
		name        string
		params      ManageAgento11yTestSuitesReadWriteParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name:   "the read operations are reachable from the write variant",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "get_suite", SuiteID: "suite-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite-1", r.URL.Path)

				writeJSONResponse(t, w, `{"suite_id":"suite-1","name":"triage regression"}`)
			},
			checkResult: func(t *testing.T, result any) {
				suite, ok := result.(*Agento11yTestSuite)
				require.True(t, ok)
				assert.Equal(t, "suite-1", suite.SuiteID)
			},
		},
		{
			name:   "create_suite sends only the fields the route declares",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "create_suite", Name: &name, Tags: &tags},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "triage regression", body["name"])
				assert.Equal(t, []any{"critical"}, body["tags"])
				// The route rejects a body field it does not know, so an unset
				// parameter must not be sent at all.
				assert.NotContains(t, body, "suite_id")
				assert.NotContains(t, body, "description")

				writeJSONResponse(t, w, `{"suite_id":"suite_generated","name":"triage regression","tags":["critical"]}`)
			},
			checkResult: func(t *testing.T, result any) {
				suite, ok := result.(*Agento11yTestSuite)
				require.True(t, ok)
				assert.Equal(t, "suite_generated", suite.SuiteID)
				assert.Empty(t, suite.LatestVersion, "a new suite has no published version yet")
			},
		},
		{
			name:   "create_suite forwards a caller-chosen suite id and a description",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "create_suite", SuiteID: "suite-1", Name: &name, Description: &description},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				assert.Equal(t, "suite-1", body["suite_id"])
				assert.Equal(t, "cases the triage agent must not regress on", body["description"])

				writeJSONResponse(t, w, `{"suite_id":"suite-1","name":"triage regression"}`)
			},
		},
		{
			name:   "update_suite renames a suite",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "update_suite", SuiteID: "suite-1", Name: &name},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				assert.Equal(t, "triage regression", body["name"])
				assert.NotContains(t, body, "description")
				assert.NotContains(t, body, "tags")

				writeJSONResponse(t, w, `{"suite_id":"suite-1","name":"triage regression"}`)
			},
		},
		{
			name:   "update_suite clears a description and a tag list explicitly",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "update_suite", SuiteID: "suite-1", Description: &emptyDescription, Tags: &noTags},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPatch, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite-1", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "", body["description"])
				assert.Equal(t, []any{}, body["tags"])
				// An absent field is left unchanged upstream, so sending it as a
				// zero value would clear it.
				assert.NotContains(t, body, "name")

				writeJSONResponse(t, w, `{"suite_id":"suite-1","name":"triage regression","versions":[{"suite_id":"suite-1","version":"v1","published":true}]}`)
			},
			checkResult: func(t *testing.T, result any) {
				suite, ok := result.(*Agento11yTestSuite)
				require.True(t, ok)
				assert.Len(t, suite.Versions, 1, "the patch response carries the version history, like get_suite")
			},
		},
		{
			name:   "create_draft_version posts to the versions collection",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "create_draft_version", SuiteID: "suite-1", Changelog: "add a regression case"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite-1/versions", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "add a regression case", body["changelog"])
				assert.Equal(t, false, body["empty_draft"])

				writeJSONResponse(t, w, `{"suite_id":"suite-1","version":"v3","test_case_count":12,"published":false,"source_version":"v2"}`)
			},
			checkResult: func(t *testing.T, result any) {
				version, ok := result.(*Agento11yTestSuiteVersion)
				require.True(t, ok)
				assert.Equal(t, "v3", version.Version)
				assert.False(t, version.Published)
				// The default clones the published version's cases.
				assert.Equal(t, 12, version.TestCaseCount)
				assert.Equal(t, "v2", version.SourceVersion)
			},
		},
		{
			name:   "create_draft_version can start from nothing",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "create_draft_version", SuiteID: "suite-1", EmptyDraft: true},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				assert.Equal(t, true, body["empty_draft"])
				assert.NotContains(t, body, "changelog")

				writeJSONResponse(t, w, `{"suite_id":"suite-1","version":"v3","test_case_count":0,"published":false}`)
			},
			checkResult: func(t *testing.T, result any) {
				version, ok := result.(*Agento11yTestSuiteVersion)
				require.True(t, ok)
				assert.Zero(t, version.TestCaseCount)
			},
		},
		{
			name:   "publish_version appends the action after the version is escaped",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "publish_version", SuiteID: "suite/1", Version: "v 3"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				// Upstream splits the last segment on the colon, so the colon must
				// stay unescaped while the version around it is escaped.
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite%2F1/versions/v%203:publish", r.URL.EscapedPath())
				// The route reads no body and takes no parameters.
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				assert.Empty(t, body)
				assert.Empty(t, r.URL.RawQuery)

				writeJSONResponse(t, w, `{"suite_id":"suite/1","version":"v 3","published":true,"published_by":"alice@grafana.com","published_at":"2026-01-05T00:00:00Z"}`)
			},
			checkResult: func(t *testing.T, result any) {
				version, ok := result.(*Agento11yTestSuiteVersion)
				require.True(t, ok)
				assert.True(t, version.Published)
				require.NotNil(t, version.PublishedAt)
			},
		},
		{
			name:   "publish_version surfaces a conflict from an already published version",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "publish_version", SuiteID: "suite-1", Version: "v1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, err := w.Write([]byte(`test suite version "v1" is already published`))
				require.NoError(t, err)
			},
			wantErr: `request failed with status 409: test suite version "v1" is already published`,
		},
		{
			name: "upsert_test_case sends the whole case",
			params: ManageAgento11yTestSuitesReadWriteParams{
				Operation:    "upsert_test_case",
				SuiteID:      "suite-1",
				Version:      "v3",
				TestCaseID:   "case-3",
				Name:         &name,
				Tags:         &tags,
				Category:     "geography",
				Input:        map[string]any{"question": "what is the capital of France"},
				Expected:     map[string]any{"answer": "Paris"},
				ArtifactRefs: []Agento11yArtifactRef{{ArtifactID: "art-1", Name: "a.png", Kind: "image"}},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite-1/versions/v3/test-cases", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "case-3", body["test_case_id"])
				assert.Equal(t, "geography", body["category"])
				assert.Equal(t, map[string]any{"question": "what is the capital of France"}, body["input"])
				assert.Equal(t, map[string]any{"answer": "Paris"}, body["expected"])
				assert.Equal(t, []any{map[string]any{"artifact_id": "art-1", "name": "a.png", "kind": "image"}}, body["artifact_refs"])
				assert.NotContains(t, body, "metadata")
				assert.NotContains(t, body, "description")

				writeJSONResponse(t, w, `{"test_case_id":"case-3","suite_id":"suite-1","suite_version":"v3","input":{"question":"what is the capital of France"}}`)
			},
			checkResult: func(t *testing.T, result any) {
				testCase, ok := result.(*Agento11yTestCase)
				require.True(t, ok)
				assert.Equal(t, "case-3", testCase.TestCaseID)
			},
		},
		{
			name: "upsert_test_case omits the id when the case is new and sends the rest",
			params: ManageAgento11yTestSuitesReadWriteParams{
				Operation:   "upsert_test_case",
				SuiteID:     "suite-1",
				Version:     "v3",
				Description: &description,
				Metadata:    map[string]any{"owner": "triage"},
				Input:       map[string]any{"question": "?"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				assert.NotContains(t, body, "test_case_id", "an omitted id has the API assign one")
				assert.Equal(t, "cases the triage agent must not regress on", body["description"])
				assert.Equal(t, map[string]any{"owner": "triage"}, body["metadata"])

				writeJSONResponse(t, w, `{"test_case_id":"case_generated","input":{"question":"?"}}`)
			},
		},
		{
			name:   "upsert_test_case surfaces a conflict from a published version",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "upsert_test_case", SuiteID: "suite-1", Version: "v1", Input: map[string]any{"question": "?"}},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, err := w.Write([]byte(`test suite version "v1" is published and immutable`))
				require.NoError(t, err)
			},
			wantErr: `request failed with status 409: test suite version "v1" is published and immutable`,
		},
		{
			name:   "delete_test_case reports the case it removed",
			params: ManageAgento11yTestSuitesReadWriteParams{Operation: "delete_test_case", SuiteID: "suite-1", Version: "v3", TestCaseID: "case-3"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite-1/versions/v3/test-cases/case-3", r.URL.Path)

				w.WriteHeader(http.StatusNoContent)
			},
			checkResult: func(t *testing.T, result any) {
				message, ok := result.(string)
				require.True(t, ok)
				assert.Contains(t, message, "case-3")
				assert.Contains(t, message, "suite-1")
			},
		},
		{
			name:    "create_suite requires a name",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_suite"},
			wantErr: "name is required for 'create_suite' operation",
		},
		{
			name:    "create_suite rejects a blank name",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_suite", Name: &blank},
			wantErr: "name is required for 'create_suite' operation",
		},
		{
			name:    "update_suite requires a suite id",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "update_suite", Name: &name},
			wantErr: `suite_id is required for "update_suite" operation`,
		},
		{
			name:    "update_suite requires at least one field",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "update_suite", SuiteID: "suite-1"},
			wantErr: "at least one of name, description, or tags is required",
		},
		{
			name:    "update_suite rejects a blank name",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "update_suite", SuiteID: "suite-1", Name: &blank},
			wantErr: "name must not be blank for 'update_suite' operation",
		},
		{
			name:    "create_draft_version requires a suite id",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_draft_version"},
			wantErr: `suite_id is required for "create_draft_version" operation`,
		},
		{
			name:    "create_draft_version rejects a caller-chosen version",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_draft_version", SuiteID: "suite-1", Version: "v9"},
			wantErr: "version must not be set for 'create_draft_version' operation",
		},
		{
			name:    "publish_version requires a version",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "publish_version", SuiteID: "suite-1"},
			wantErr: `version is required for "publish_version" operation`,
		},
		{
			name:    "upsert_test_case requires a version",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "upsert_test_case", SuiteID: "suite-1", Input: map[string]any{"question": "?"}},
			wantErr: `version is required for "upsert_test_case" operation`,
		},
		{
			name:    "upsert_test_case requires an input",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "upsert_test_case", SuiteID: "suite-1", Version: "v3"},
			wantErr: "input is required and must not be empty for 'upsert_test_case' operation",
		},
		{
			name:    "upsert_test_case rejects an empty input",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "upsert_test_case", SuiteID: "suite-1", Version: "v3", Input: map[string]any{}},
			wantErr: "input is required and must not be empty for 'upsert_test_case' operation",
		},
		{
			name:    "delete_test_case requires a test case id",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "delete_test_case", SuiteID: "suite-1", Version: "v3"},
			wantErr: "test_case_id is required for 'delete_test_case' operation",
		},
		{
			name:    "unknown operation names every operation the variant has",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "delete_suite"},
			wantErr: `unknown operation "delete_suite", must be one of: ` + agento11yTestSuiteAllOperations,
		},
		{
			name:    "reading one version on its own is not an operation on the write variant either",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "get_version", SuiteID: "suite-1", Version: "v2"},
			wantErr: `unknown operation "get_version"`,
		},
		{
			name:    "read validation still applies on the write variant",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "get_test_case", SuiteID: "suite-1", Version: "v2"},
			wantErr: `test_case_id is required for "get_test_case" operation`,
		},
		{
			// The write fields are declared on this variant, so a read operation
			// would otherwise accept them and drop them: 'tags' on 'list_suites'
			// reads like a filter and would answer with every suite in the tenant.
			name:    "a read operation rejects a write tag list rather than dropping it",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "list_suites", Tags: &tags},
			wantErr: `tags is only read by the write operations, not by "list_suites"`,
		},
		{
			name:    "a read operation rejects a write name",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "get_suite", SuiteID: "suite-1", Name: &name},
			wantErr: `name is only read by the write operations, not by "get_suite"`,
		},
		{
			name:    "a read operation rejects a test case body",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "list_test_cases", SuiteID: "suite-1", Version: "v2", Input: map[string]any{"question": "?"}},
			wantErr: `input is only read by the write operations, not by "list_test_cases"`,
		},
		{
			// create_suite creates an empty suite that has no version to hold a
			// case, so a case body would be dropped and the 200 would read as proof
			// the case was stored.
			name:    "create_suite rejects a test case body rather than dropping it",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_suite", Name: &name, Input: map[string]any{"question": "?"}},
			wantErr: `input is not accepted by "create_suite" operation, which would drop it: it is only read by 'upsert_test_case'`,
		},
		{
			name:    "create_suite rejects a changelog",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_suite", Name: &name, Changelog: "first cut"},
			wantErr: `changelog is not accepted by "create_suite" operation, which would drop it: it is only read by 'create_draft_version'`,
		},
		{
			name:    "create_suite rejects a version it cannot create",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_suite", Name: &name, Version: "v1"},
			wantErr: `version is not accepted by "create_suite" operation, which would drop it: it is only read by 'publish_version', 'upsert_test_case', 'delete_test_case'`,
		},
		{
			name:    "create_draft_version rejects a test case body",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_draft_version", SuiteID: "suite-1", Expected: map[string]any{"answer": "ok"}},
			wantErr: `expected is not accepted by "create_draft_version" operation, which would drop it: it is only read by 'upsert_test_case'`,
		},
		{
			name:    "create_draft_version rejects a suite name",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_draft_version", SuiteID: "suite-1", Name: &name},
			wantErr: `name is not accepted by "create_draft_version" operation, which would drop it: it is only read by 'create_suite', 'update_suite', 'upsert_test_case'`,
		},
		{
			name:    "create_draft_version rejects a test case id",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "create_draft_version", SuiteID: "suite-1", TestCaseID: "case-3"},
			wantErr: `test_case_id is not accepted by "create_draft_version" operation, which would drop it: it is only read by 'upsert_test_case', 'delete_test_case'`,
		},
		{
			name:    "update_suite rejects a test case body",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "update_suite", SuiteID: "suite-1", Name: &name, Category: "routing"},
			wantErr: `category is not accepted by "update_suite" operation, which would drop it: it is only read by 'upsert_test_case'`,
		},
		{
			// publish_version freezes the whole version and sends no body at all.
			name:    "publish_version rejects a test case body",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "publish_version", SuiteID: "suite-1", Version: "v3", Input: map[string]any{"question": "?"}},
			wantErr: `input is not accepted by "publish_version" operation, which would drop it: it is only read by 'upsert_test_case'`,
		},
		{
			name:    "upsert_test_case rejects a version changelog",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "upsert_test_case", SuiteID: "suite-1", Version: "v3", Input: map[string]any{"question": "?"}, Changelog: "add a case"},
			wantErr: `changelog is not accepted by "upsert_test_case" operation, which would drop it: it is only read by 'create_draft_version'`,
		},
		{
			name:    "delete_test_case rejects an empty_draft flag",
			params:  ManageAgento11yTestSuitesReadWriteParams{Operation: "delete_test_case", SuiteID: "suite-1", Version: "v3", TestCaseID: "case-3", EmptyDraft: true},
			wantErr: `empty_draft is not accepted by "delete_test_case" operation, which would drop it: it is only read by 'create_draft_version'`,
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

			result, err := manageAgento11yTestSuitesReadWrite(ctx, tc.params)
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

// TestAgento11yTestSuiteRoutesAvoidTheStandaloneVersion pins the one proxied
// route this tool deliberately does not call. The plugin proxy forwards a GET
// on a single version, but upstream has no handler for it and answers 400, so
// version history has to come from the versions list get_suite embeds.
//
// The check is on the path each operation builds, because a client method that
// dropped the /test-cases or :publish suffix would still compile and would then
// address exactly that route.
func TestAgento11yTestSuiteRoutesAvoidTheStandaloneVersion(t *testing.T) {
	const standalone = "/api/plugins/grafana-agento11y-app/resources/eval/test-suites/suite-1/versions/v2"

	for _, params := range []ManageAgento11yTestSuitesReadWriteParams{
		{Operation: "get_suite", SuiteID: "suite-1"},
		{Operation: "list_test_cases", SuiteID: "suite-1", Version: "v2"},
		{Operation: "get_test_case", SuiteID: "suite-1", Version: "v2", TestCaseID: "case-3"},
		{Operation: "publish_version", SuiteID: "suite-1", Version: "v2"},
		{Operation: "upsert_test_case", SuiteID: "suite-1", Version: "v2", Input: map[string]any{"question": "?"}},
		{Operation: "delete_test_case", SuiteID: "suite-1", Version: "v2", TestCaseID: "case-3"},
	} {
		t.Run(params.Operation, func(t *testing.T) {
			var requested []string
			server, ctx := setupMockAgento11yServer(func(w http.ResponseWriter, r *http.Request) {
				requested = append(requested, r.URL.Path)
				writeJSONResponse(t, w, `{"suite_id":"suite-1","version":"v2","test_case_id":"case-3","input":{},"items":[],"next_cursor":""}`)
			})
			defer server.Close()

			_, err := manageAgento11yTestSuitesReadWrite(ctx, params)
			require.NoError(t, err)
			require.Len(t, requested, 1, "each operation makes exactly one request")
			assert.NotEqual(t, standalone, requested[0], "the standalone version route answers 400 upstream")
		})
	}
}

// TestAgento11yManageTestSuitesToolContract pins the parts of the advertised
// tool a client relies on but no request test touches: the annotations that
// tell a caller whether it is safe to run, and the two rules a caller cannot
// discover from a successful response.
func TestAgento11yManageTestSuitesToolContract(t *testing.T) {
	read := ManageAgento11yTestSuitesRead.Tool
	require.NotNil(t, read.Annotations.ReadOnlyHint)
	assert.True(t, *read.Annotations.ReadOnlyHint)
	require.NotNil(t, read.Annotations.IdempotentHint)
	assert.True(t, *read.Annotations.IdempotentHint)
	require.NotNil(t, read.Annotations.DestructiveHint)
	assert.False(t, *read.Annotations.DestructiveHint)

	write := ManageAgento11yTestSuitesReadWrite.Tool
	require.NotNil(t, write.Annotations.ReadOnlyHint)
	assert.False(t, *write.Annotations.ReadOnlyHint, "the write variant deletes test cases and freezes versions")
	require.NotNil(t, write.Annotations.DestructiveHint)
	assert.True(t, *write.Annotations.DestructiveHint)

	for _, guidance := range []string{
		// A caller that patches one field of a test case loses the rest, and a
		// successful response looks the same either way.
		"replaces the stored case rather than merging into it",
		// Publishing is one-way, which is the fact that makes the draft step
		// worth taking.
		"There is no unpublish",
		// A 403 is unreadable without the role that causes it.
		"grafana-agento11y-app.eval:write",
	} {
		assert.Contains(t, write.Description, guidance)
	}

	// A published version rejects every edit, so a caller has to know before it
	// builds one.
	for _, tool := range []string{read.Description, write.Description} {
		assert.Contains(t, tool, "publishing freezes it")
		assert.Contains(t, tool, "grafana-agento11y-app.data:read")
	}
}
