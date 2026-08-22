//go:build unit
// +build unit

package tools

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgento11yEvalsReadEvalCollections(t *testing.T) {
	testCases := []struct {
		name            string
		params          Agento11yEvalsReadParams
		handler         func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr         string
		wantErrExcludes string // text the error must not carry, for a remap that should not fire
		checkResult     func(t *testing.T, result any)
	}{
		{
			name:   "list saved conversations sends the default limit and keeps total_count",
			params: Agento11yEvalsReadParams{Operation: "list_saved_conversations"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/saved-conversations", r.URL.Path)
				require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
				require.Equal(t, "50", r.URL.Query().Get("limit"))
				require.False(t, r.URL.Query().Has("cursor"))
				require.False(t, r.URL.Query().Has("source"))

				writeJSONResponse(t, w, `{"items":[{
					"saved_id": "saved-conv-1",
					"conversation_id": "conv-1",
					"name": "Failure",
					"source": "telemetry",
					"tags": {"team": "triage"},
					"generation_count": 4,
					"total_tokens": 1200,
					"agent_names": ["triage"],
					"models": ["gpt-5"],
					"collections": [{"collection_id": "collection-1", "name": "Regression set"}]
				}],"next_cursor":"120","total_count":475}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*Agento11ySavedConversationsResponse)
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "120", resp.NextCursor)
				assert.Equal(t, int64(475), resp.TotalCount)

				saved := resp.Items[0]
				assert.Equal(t, "saved-conv-1", saved.SavedID)
				assert.Equal(t, "conv-1", saved.ConversationID)
				assert.Equal(t, "telemetry", saved.Source)
				assert.Equal(t, map[string]string{"team": "triage"}, saved.Tags)
				assert.Equal(t, 4, saved.GenerationCount)
				assert.Equal(t, int64(1200), saved.TotalTokens)
				assert.Equal(t, []string{"triage"}, saved.AgentNames)
				assert.Equal(t, []string{"gpt-5"}, saved.Models)
				require.NotNil(t, saved.Collections)
				require.Len(t, *saved.Collections, 1)
				assert.Equal(t, "collection-1", (*saved.Collections)[0].CollectionID)
				assert.Equal(t, "Regression set", (*saved.Collections)[0].Name)
			},
		},
		{
			name: "list saved conversations forwards limit, cursor, and source",
			params: Agento11yEvalsReadParams{
				Operation: "list_saved_conversations",
				Limit:     25, Cursor: "120", Source: "telemetry",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "25", r.URL.Query().Get("limit"))
				require.Equal(t, "120", r.URL.Query().Get("cursor"))
				require.Equal(t, "telemetry", r.URL.Query().Get("source"))

				writeJSONResponse(t, w, `{"items":[],"next_cursor":"","total_count":0}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*Agento11ySavedConversationsResponse)
				require.True(t, ok)
				assert.Empty(t, resp.Items)
				assert.Empty(t, resp.NextCursor, "the tool must not fetch another page on its own")
			},
		},
		{
			name:   "an explicitly empty collections array stays non-nil",
			params: Agento11yEvalsReadParams{Operation: "list_saved_conversations"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				writeJSONResponse(t, w, `{"items":[
					{"saved_id":"saved-empty","collections":[]},
					{"saved_id":"saved-absent"}
				],"next_cursor":"","total_count":2}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*Agento11ySavedConversationsResponse)
				require.True(t, ok)
				require.Len(t, resp.Items, 2)

				// See the Collections field comment: the two states are not the same.
				require.NotNil(t, resp.Items[0].Collections)
				assert.Empty(t, *resp.Items[0].Collections)
				assert.Nil(t, resp.Items[1].Collections)
			},
		},
		{
			name:   "get saved conversation",
			params: Agento11yEvalsReadParams{Operation: "get_saved_conversation", SavedID: "saved-conv-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/saved-conversations/saved-conv-1", r.URL.Path)
				require.Empty(t, r.URL.RawQuery)

				writeJSONResponse(t, w, `{"saved_id":"saved-conv-1","conversation_id":"conv-1","name":"Failure","source":"telemetry"}`)
			},
			checkResult: func(t *testing.T, result any) {
				saved, ok := result.(*Agento11ySavedConversation)
				require.True(t, ok)
				assert.Equal(t, "saved-conv-1", saved.SavedID)
				assert.Nil(t, saved.Collections, "the get route does not enrich collections")
			},
		},
		{
			name:   "missing saved conversation reports the id the bare 404 body omits",
			params: Agento11yEvalsReadParams{Operation: "get_saved_conversation", SavedID: "saved-missing"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			},
			// The cause is wrapped, so the status stays inspectable.
			wantErr: `saved conversation "saved-missing" not found: request failed with status 404: 404 page not found`,
		},
		{
			name:   "a descriptive 404 on the same route is passed through, not remapped",
			params: Agento11yEvalsReadParams{Operation: "get_saved_conversation", SavedID: "saved-conv-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				// What the plugin proxy answers when the app is not installed, and
				// what an older plugin build answers for a route it does not have.
				w.WriteHeader(http.StatusNotFound)
				_, err := w.Write([]byte(`Plugin not found, no installed plugin with id grafana-agento11y-app`))
				require.NoError(t, err)
			},
			wantErr:         "request failed with status 404: Plugin not found",
			wantErrExcludes: "saved conversation",
		},
		{
			name:   "list collections for a saved conversation",
			params: Agento11yEvalsReadParams{Operation: "list_collections_for_saved_conversation", SavedID: "saved-conv-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/saved-conversations/saved-conv-1/collections", r.URL.Path)
				require.Empty(t, r.URL.RawQuery, "the reverse lookup is unpaginated")

				writeJSONResponse(t, w, `{"items":[{"collection_id":"collection-1","name":"Regression set","member_count":3}],"next_cursor":""}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yCollection])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "collection-1", resp.Items[0].CollectionID)
				assert.Equal(t, 3, resp.Items[0].MemberCount)
			},
		},
		{
			name:   "list collections",
			params: Agento11yEvalsReadParams{Operation: "list_collections", Limit: 10, Cursor: "collection-9"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/collections", r.URL.Path)
				require.Equal(t, "10", r.URL.Query().Get("limit"))
				require.Equal(t, "collection-9", r.URL.Query().Get("cursor"))
				require.False(t, r.URL.Query().Has("source"), "source only exists on the saved conversation list")

				writeJSONResponse(t, w, `{"items":[{"collection_id":"collection-1","name":"Regression set","description":"failures worth re-running","member_count":3}],"next_cursor":"collection-1"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11yCollection])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "failures worth re-running", resp.Items[0].Description)
				assert.Equal(t, "collection-1", resp.NextCursor)
			},
		},
		{
			name:   "list collections ignores a source filter it cannot use",
			params: Agento11yEvalsReadParams{Operation: "list_collections", Source: "manual"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.False(t, r.URL.Query().Has("source"))

				writeJSONResponse(t, w, `{"items":[]}`)
			},
		},
		{
			name:   "get collection",
			params: Agento11yEvalsReadParams{Operation: "get_collection", CollectionID: "collection-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/collections/collection-1", r.URL.Path)

				writeJSONResponse(t, w, `{"collection_id":"collection-1","name":"Regression set","member_count":3}`)
			},
			checkResult: func(t *testing.T, result any) {
				collection, ok := result.(*Agento11yCollection)
				require.True(t, ok)
				assert.Equal(t, "collection-1", collection.CollectionID)
				assert.Equal(t, 3, collection.MemberCount)
			},
		},
		{
			name:   "unknown collection surfaces the descriptive 404 body",
			params: Agento11yEvalsReadParams{Operation: "get_collection", CollectionID: "collection-missing"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, err := w.Write([]byte(`collection "collection-missing" not found`))
				require.NoError(t, err)
			},
			wantErr: `request failed with status 404: collection "collection-missing" not found`,
		},
		{
			name: "list collection members forwards its own cursor space",
			params: Agento11yEvalsReadParams{
				Operation:    "list_collection_members",
				CollectionID: "collection-1",
				Cursor:       "saved-conv-9",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/collections/collection-1/members", r.URL.Path)
				require.Equal(t, "saved-conv-9", r.URL.Query().Get("cursor"))
				require.Equal(t, "50", r.URL.Query().Get("limit"))

				writeJSONResponse(t, w, `{"items":[{"saved_id":"saved-conv-1","name":"Failure","generation_count":4,"collections":[{"collection_id":"collection-1","name":"Regression set"}]}],"next_cursor":"saved-conv-1"}`)
			},
			checkResult: func(t *testing.T, result any) {
				resp, ok := result.(*agento11yListResponse[Agento11ySavedConversation])
				require.True(t, ok)
				require.Len(t, resp.Items, 1)
				assert.Equal(t, "saved-conv-1", resp.Items[0].SavedID)
				require.NotNil(t, resp.Items[0].Collections)
				require.Len(t, *resp.Items[0].Collections, 1)
				assert.Equal(t, "saved-conv-1", resp.NextCursor)
			},
		},
		{
			name:    "get saved conversation without saved_id",
			params:  Agento11yEvalsReadParams{Operation: "get_saved_conversation"},
			wantErr: `saved_id is required for "get_saved_conversation"`,
		},
		{
			name:    "list collections for a saved conversation without saved_id",
			params:  Agento11yEvalsReadParams{Operation: "list_collections_for_saved_conversation"},
			wantErr: `saved_id is required for "list_collections_for_saved_conversation"`,
		},
		{
			name:    "get collection without collection_id",
			params:  Agento11yEvalsReadParams{Operation: "get_collection"},
			wantErr: `collection_id is required for "get_collection"`,
		},
		{
			name:    "list collection members without collection_id",
			params:  Agento11yEvalsReadParams{Operation: "list_collection_members"},
			wantErr: `collection_id is required for "list_collection_members"`,
		},
		{
			name:    "unsupported source filter",
			params:  Agento11yEvalsReadParams{Operation: "list_saved_conversations", Source: "telemetryy"},
			wantErr: `unknown source "telemetryy", must be one of: telemetry, manual`,
		},
		{
			name:    "write operation is rejected by the read variant",
			params:  Agento11yEvalsReadParams{Operation: "save_conversation"},
			wantErr: "unknown operation",
		},
		{
			name:    "unknown operation lists the read operations",
			params:  Agento11yEvalsReadParams{Operation: "list"},
			wantErr: `unknown operation "list", must be one of: ` + agento11yEvalsReadOperations,
		},
		{
			name:   "permission error surfaces the plugin body",
			params: Agento11yEvalsReadParams{Operation: "list_collections"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte(`permission denied: grafana-agento11y-app.data:read required`))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 403: permission denied: grafana-agento11y-app.data:read required",
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

			result, err := agento11yEvalsRead(ctx, tc.params)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				if tc.wantErrExcludes != "" {
					assert.NotContains(t, err.Error(), tc.wantErrExcludes)
				}
				return
			}
			require.NoError(t, err)
			if tc.checkResult != nil {
				tc.checkResult(t, result)
			}
		})
	}
}

func TestAgento11yEvalsWriteEvalCollections(t *testing.T) {
	emptyDescription := ""
	description := "failures worth re-running"

	testCases := []struct {
		name        string
		params      Agento11yEvalsWriteParams
		handler     func(t *testing.T, w http.ResponseWriter, r *http.Request) // nil: server must not be called
		wantErr     string
		checkResult func(t *testing.T, result any)
	}{
		{
			name: "save conversation derives the saved id and omits server-owned fields",
			params: Agento11yEvalsWriteParams{
				Operation:      "save_conversation",
				ConversationID: "conv-1",
				Name:           agento11yPtr("Failure"),
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/saved-conversations", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "saved-conv-1", body["saved_id"])
				assert.Equal(t, "conv-1", body["conversation_id"])
				assert.Equal(t, "Failure", body["name"])
				assert.NotContains(t, body, "saved_by", "saved_by comes from the caller identity")
				assert.NotContains(t, body, "tags", "an empty tag map is omitted")

				writeJSONResponse(t, w, `{"saved_id":"saved-conv-1","conversation_id":"conv-1","name":"Failure","source":"telemetry"}`)
			},
			checkResult: func(t *testing.T, result any) {
				saved, ok := result.(*Agento11ySavedConversation)
				require.True(t, ok)
				assert.Equal(t, "saved-conv-1", saved.SavedID)
			},
		},
		{
			name: "save conversation keeps an explicit saved id and sends tags",
			params: Agento11yEvalsWriteParams{
				Operation:        "save_conversation",
				SavedID:          "regression:2026-04-23",
				ConversationID:   "conv-1",
				Name:             agento11yPtr("Failure"),
				ConversationTags: map[string]string{"team": "triage"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				assert.Equal(t, "regression:2026-04-23", body["saved_id"])
				assert.Equal(t, map[string]any{"team": "triage"}, body["tags"])

				writeJSONResponse(t, w, `{"saved_id":"regression:2026-04-23","conversation_id":"conv-1","name":"Failure"}`)
			},
		},
		{
			name: "save conversation with an empty tag map omits tags",
			params: Agento11yEvalsWriteParams{
				Operation:        "save_conversation",
				ConversationID:   "conv-1",
				Name:             agento11yPtr("Failure"),
				ConversationTags: map[string]string{},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				assert.NotContains(t, body, "tags")
				assert.Equal(t, "saved-conv-1", body["saved_id"])
				assert.Equal(t, "conv-1", body["conversation_id"])
				assert.Equal(t, "Failure", body["name"])

				writeJSONResponse(t, w, `{"saved_id":"saved-conv-1"}`)
			},
		},
		{
			name: "an already saved conversation surfaces the 409 body",
			params: Agento11yEvalsWriteParams{
				Operation:      "save_conversation",
				ConversationID: "conv-1",
				Name:           agento11yPtr("Failure"),
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, err := w.Write([]byte(`conversation "conv-1" is already saved as "saved-conv-1"`))
				require.NoError(t, err)
			},
			wantErr: `request failed with status 409: conversation "conv-1" is already saved as "saved-conv-1"`,
		},
		{
			name:   "delete saved conversation handles 204 with an empty body",
			params: Agento11yEvalsWriteParams{Operation: "delete_saved_conversation", SavedID: "saved-conv-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/saved-conversations/saved-conv-1", r.URL.Path)

				w.WriteHeader(http.StatusNoContent)
			},
			checkResult: func(t *testing.T, result any) {
				message, ok := result.(string)
				require.True(t, ok)
				assert.Contains(t, message, "saved-conv-1")
				assert.Contains(t, message, "deleted successfully")
				assert.Contains(t, message, "collection", "the receipt should say memberships are gone too")
			},
		},
		{
			name: "create collection omits the server-assigned id",
			params: Agento11yEvalsWriteParams{
				Operation: "create_collection",
				Name:      agento11yPtr("Regression set"),
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/collections", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "Regression set", body["name"])
				assert.NotContains(t, body, "collection_id", "the API assigns the collection id")
				assert.NotContains(t, body, "created_by", "created_by comes from the caller identity")
				assert.NotContains(t, body, "description")

				writeJSONResponse(t, w, `{"collection_id":"018f2c6e-2f1a-7c2b-9a3d-6f1c2b9a3d6f","name":"Regression set","member_count":0}`)
			},
			checkResult: func(t *testing.T, result any) {
				collection, ok := result.(*Agento11yCollection)
				require.True(t, ok)
				assert.Equal(t, "018f2c6e-2f1a-7c2b-9a3d-6f1c2b9a3d6f", collection.CollectionID)
			},
		},
		{
			name: "create collection sends a supplied description",
			params: Agento11yEvalsWriteParams{
				Operation:   "create_collection",
				Name:        agento11yPtr("Regression set"),
				Description: &description,
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				assert.Equal(t, "failures worth re-running", body["description"])

				writeJSONResponse(t, w, `{"collection_id":"collection-1","name":"Regression set","description":"failures worth re-running"}`)
			},
		},
		{
			name: "update collection patches only the fields it was given",
			params: Agento11yEvalsWriteParams{
				Operation:    "update_collection",
				CollectionID: "collection-1",
				Name:         agento11yPtr("Renamed"),
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPatch, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/collections/collection-1", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, "Renamed", body["name"])
				assert.NotContains(t, body, "description", "an absent description must stay unchanged")
				assert.NotContains(t, body, "updated_by")

				writeJSONResponse(t, w, `{"collection_id":"collection-1","name":"Renamed","member_count":3}`)
			},
			checkResult: func(t *testing.T, result any) {
				collection, ok := result.(*Agento11yCollection)
				require.True(t, ok)
				assert.Equal(t, "Renamed", collection.Name)
			},
		},
		{
			name: "update collection sends an explicitly empty description to clear it",
			params: Agento11yEvalsWriteParams{
				Operation:    "update_collection",
				CollectionID: "collection-1",
				Description:  &emptyDescription,
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				body := decodeRequestBody(t, r)
				require.Contains(t, body, "description")
				assert.Equal(t, "", body["description"])
				assert.NotContains(t, body, "name")

				writeJSONResponse(t, w, `{"collection_id":"collection-1","name":"Regression set"}`)
			},
			checkResult: func(t *testing.T, result any) {
				collection, ok := result.(*Agento11yCollection)
				require.True(t, ok)
				assert.Empty(t, collection.Description)
			},
		},
		{
			name:   "delete collection handles 204 with an empty body",
			params: Agento11yEvalsWriteParams{Operation: "delete_collection", CollectionID: "collection-1"},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/collections/collection-1", r.URL.Path)

				w.WriteHeader(http.StatusNoContent)
			},
			checkResult: func(t *testing.T, result any) {
				message, ok := result.(string)
				require.True(t, ok)
				assert.Equal(t, "Collection collection-1 deleted successfully", message)
			},
		},
		{
			name: "add collection members turns the status body into a receipt",
			params: Agento11yEvalsWriteParams{
				Operation:    "add_collection_members",
				CollectionID: "collection-1",
				SavedIDs:     []string{"saved-conv-1", "saved-conv-2"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/collections/collection-1/members", r.URL.Path)

				body := decodeRequestBody(t, r)
				assert.Equal(t, []any{"saved-conv-1", "saved-conv-2"}, body["saved_ids"])
				assert.NotContains(t, body, "added_by", "added_by comes from the caller identity")
				assert.Len(t, body, 1)

				writeJSONResponse(t, w, `{"status":"ok"}`)
			},
			checkResult: func(t *testing.T, result any) {
				message, ok := result.(string)
				require.True(t, ok)
				assert.Equal(t, "Collection collection-1 now contains the 2 requested saved conversation(s)", message)
			},
		},
		{
			name: "an unknown saved id surfaces the 400 body",
			params: Agento11yEvalsWriteParams{
				Operation:    "add_collection_members",
				CollectionID: "collection-1",
				SavedIDs:     []string{"saved-missing"},
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, err := w.Write([]byte(`saved conversation "saved-missing" not found`))
				require.NoError(t, err)
			},
			wantErr: `request failed with status 400: saved conversation "saved-missing" not found`,
		},
		{
			name: "remove collection member handles 204 with an empty body",
			params: Agento11yEvalsWriteParams{
				Operation:    "remove_collection_member",
				CollectionID: "collection-1",
				SavedID:      "saved-conv-1",
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/api/plugins/grafana-agento11y-app/resources/eval/collections/collection-1/members/saved-conv-1", r.URL.Path)

				w.WriteHeader(http.StatusNoContent)
			},
			checkResult: func(t *testing.T, result any) {
				message, ok := result.(string)
				require.True(t, ok)
				assert.Equal(t, "Saved conversation saved-conv-1 removed from collection collection-1 successfully", message)
			},
		},
		{
			// Under the disjoint-operation split, agento11y_evals_write no
			// longer reaches the read dispatch for a sibling read operation -
			// see TestAgento11yEvalsWriteRejectsReadOperations in
			// agento11y_evals_test.go for the general case across every domain.
			name:    "a read operation is rejected, not dispatched",
			params:  Agento11yEvalsWriteParams{Operation: "list_saved_conversations"},
			wantErr: `"list_saved_conversations" is an agento11y_evals_read operation, not agento11y_evals_write`,
		},
		{
			name:    "save conversation without conversation_id",
			params:  Agento11yEvalsWriteParams{Operation: "save_conversation", Name: agento11yPtr("Failure")},
			wantErr: "conversation_id is required for 'save_conversation'",
		},
		{
			name:    "save conversation without a name",
			params:  Agento11yEvalsWriteParams{Operation: "save_conversation", ConversationID: "conv-1"},
			wantErr: "name is required for 'save_conversation'",
		},
		{
			name: "save conversation with an invalid saved_id",
			params: Agento11yEvalsWriteParams{
				Operation:      "save_conversation",
				SavedID:        "saved conversation",
				ConversationID: "conv-1",
				Name:           agento11yPtr("Failure"),
			},
			wantErr: `saved_id "saved conversation" is invalid`,
		},
		{
			name: "save conversation whose conversation_id derives an invalid saved_id",
			params: Agento11yEvalsWriteParams{
				Operation:      "save_conversation",
				ConversationID: "conv 1/2",
				Name:           agento11yPtr("Failure"),
			},
			wantErr: `the saved_id derived from conversation_id, "saved-conv 1/2", is invalid`,
		},
		{
			name:    "delete saved conversation without saved_id",
			params:  Agento11yEvalsWriteParams{Operation: "delete_saved_conversation"},
			wantErr: "saved_id is required for 'delete_saved_conversation'",
		},
		{
			name:    "create collection without a name",
			params:  Agento11yEvalsWriteParams{Operation: "create_collection"},
			wantErr: "name is required for 'create_collection'",
		},
		{
			// The API creates an empty collection, so accepting saved_ids here
			// would report success for members that were never added.
			name:    "create collection with saved_ids it would drop",
			params:  Agento11yEvalsWriteParams{Operation: "create_collection", Name: agento11yPtr("Regression set"), SavedIDs: []string{"saved-conv-1"}},
			wantErr: "saved_ids is not accepted by 'create_collection' operation",
		},
		{
			// The ID is server-assigned, so accepting one here would hand back a
			// collection the caller cannot address by the ID it asked for.
			name:    "create collection with a collection_id it would drop",
			params:  Agento11yEvalsWriteParams{Operation: "create_collection", Name: agento11yPtr("Regression set"), CollectionID: "collection-1"},
			wantErr: "collection_id is not accepted by 'create_collection' operation",
		},
		{
			name: "save conversation with a collection_id it would drop",
			params: Agento11yEvalsWriteParams{
				Operation:      "save_conversation",
				ConversationID: "conv-1",
				Name:           agento11yPtr("Failure"),
				CollectionID:   "collection-1",
			},
			wantErr: "collection_id is not accepted by 'save_conversation' operation",
		},
		{
			name:    "update collection without collection_id",
			params:  Agento11yEvalsWriteParams{Operation: "update_collection", Name: agento11yPtr("Renamed")},
			wantErr: "collection_id is required for 'update_collection'",
		},
		{
			name:    "update collection without any field to change",
			params:  Agento11yEvalsWriteParams{Operation: "update_collection", CollectionID: "collection-1"},
			wantErr: "at least one of name or description is required for 'update_collection'",
		},
		{
			name:    "update collection with a blank name",
			params:  Agento11yEvalsWriteParams{Operation: "update_collection", CollectionID: "collection-1", Name: agento11yPtr("   ")},
			wantErr: "name must not be blank for 'update_collection'",
		},
		{
			name:    "delete collection without collection_id",
			params:  Agento11yEvalsWriteParams{Operation: "delete_collection"},
			wantErr: "collection_id is required for 'delete_collection'",
		},
		{
			name:    "add collection members without collection_id",
			params:  Agento11yEvalsWriteParams{Operation: "add_collection_members", SavedIDs: []string{"saved-conv-1"}},
			wantErr: "collection_id is required for 'add_collection_members'",
		},
		{
			name:    "add collection members with an empty saved id list",
			params:  Agento11yEvalsWriteParams{Operation: "add_collection_members", CollectionID: "collection-1", SavedIDs: []string{}},
			wantErr: "saved_ids is required for 'add_collection_members'",
		},
		{
			name:    "remove collection member without saved_id",
			params:  Agento11yEvalsWriteParams{Operation: "remove_collection_member", CollectionID: "collection-1"},
			wantErr: "saved_id is required for 'remove_collection_member'",
		},
		{
			name:    "unknown operation lists every write operation",
			params:  Agento11yEvalsWriteParams{Operation: "bookmark_conversation"},
			wantErr: `unknown operation "bookmark_conversation", must be one of: ` + agento11yEvalsWriteOperations,
		},
		{
			// list_saved_conversations is a read operation: under the
			// disjoint-operation split it no longer exists on the write side
			// at all (there is no Source field here to reject a bad filter
			// value on), so the write tool reports it as belonging to the
			// sibling read tool rather than as a source-validation failure.
			name:    "a sibling read operation is named specifically, not treated as unknown",
			params:  Agento11yEvalsWriteParams{Operation: "list_saved_conversations"},
			wantErr: `"list_saved_conversations" is an agento11y_evals_read operation, not agento11y_evals_write`,
		},
		{
			name: "write permission error surfaces the plugin body",
			params: Agento11yEvalsWriteParams{
				Operation: "create_collection",
				Name:      agento11yPtr("Regression set"),
			},
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte(`permission denied: grafana-agento11y-app.eval:write required`))
				require.NoError(t, err)
			},
			wantErr: "request failed with status 403: permission denied: grafana-agento11y-app.eval:write required",
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

			result, err := agento11yEvalsWrite(ctx, tc.params)
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
