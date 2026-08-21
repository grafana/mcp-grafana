//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockCtxWithClient(server *httptest.Server) context.Context {
	u, _ := url.Parse(server.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"

	c := client.NewHTTPClientWithConfig(nil, cfg)
	return mcpgrafana.WithGrafanaClient(context.Background(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c})
}

func ptr[T any](v T) *T { return &v }

// --- annotations_read tool definition and validation ---

func TestAnnotationsReadToolDefinition(t *testing.T) {
	require.NotNil(t, AnnotationsRead)
	assert.Equal(t, "annotations_read", AnnotationsRead.Tool.Name)
	for _, op := range annotationsReadOperations {
		assert.Contains(t, AnnotationsRead.Tool.Description, op)
	}
}

func TestAnnotationsReadParams_Validate(t *testing.T) {
	require.NoError(t, AnnotationsReadParams{Operation: "list"}.validate())
	require.NoError(t, AnnotationsReadParams{Operation: "tags"}.validate())

	err := AnnotationsReadParams{Operation: "bogus"}.validate()
	require.Error(t, err)
	assert.EqualError(t, err, `unknown operation "bogus", must be one of: list, tags`)
}

// --- annotations_write tool definition and validation ---

func TestAnnotationsWriteToolDefinition(t *testing.T) {
	require.NotNil(t, AnnotationsWrite)
	assert.Equal(t, "annotations_write", AnnotationsWrite.Tool.Name)
	for _, op := range annotationsWriteOperations {
		assert.Contains(t, AnnotationsWrite.Tool.Description, op)
	}
}

func TestAnnotationsWriteParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  AnnotationsWriteParams
		wantErr string
	}{
		{name: "create with text", params: AnnotationsWriteParams{Operation: "create", Text: ptr("hello")}},
		{
			name:    "create missing text",
			params:  AnnotationsWriteParams{Operation: "create"},
			wantErr: "text is required for 'create' operation",
		},
		{
			name:    "create graphite missing what",
			params:  AnnotationsWriteParams{Operation: "create", Format: "graphite"},
			wantErr: "what is required for 'create' operation",
		},
		{
			name:   "create graphite with what does not require text",
			params: AnnotationsWriteParams{Operation: "create", Format: "graphite", What: "deploy"},
		},
		{name: "update with id", params: AnnotationsWriteParams{Operation: "update", ID: 9}},
		{
			name:    "update missing id",
			params:  AnnotationsWriteParams{Operation: "update"},
			wantErr: "id is required for 'update' operation",
		},
		{name: "delete with id", params: AnnotationsWriteParams{Operation: "delete", ID: 9}},
		{
			name:    "delete missing id",
			params:  AnnotationsWriteParams{Operation: "delete"},
			wantErr: "id is required for 'delete' operation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestAnnotationsWriteParams_Validate_RejectsReadOperations(t *testing.T) {
	// A schema-conformant caller can never send "list" or "tags" to
	// annotations_write (its jsonschema enum doesn't offer them), but the
	// Go-level validate() must still reject them explicitly rather than
	// silently succeeding — see AnnotationsWriteParams.validate's doc
	// comment for why delegating dispatch here would be wrong.
	for _, op := range annotationsReadOperations {
		err := AnnotationsWriteParams{Operation: op}.validate()
		require.Error(t, err, "operation %q must not validate for annotations_write", op)
		assert.Contains(t, err.Error(), "annotations_read operation, not annotations_write")
	}
}

func TestAnnotationsWriteParams_Validate_UnknownOperationListsAllOperations(t *testing.T) {
	err := AnnotationsWriteParams{Operation: "bogus"}.validate()
	require.Error(t, err)
	assert.EqualError(t, err, `unknown operation "bogus", must be one of: list, tags, create, update, delete`)
}

// --- dispatch ---

func TestAnnotationsReadDispatch_UnknownOperationDoesNotPanic(t *testing.T) {
	_, err := annotationsRead(context.Background(), AnnotationsReadParams{Operation: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "annotations_read")
}

func TestAnnotationsWriteDispatch_UnknownOperationDoesNotPanic(t *testing.T) {
	_, err := annotationsWrite(context.Background(), AnnotationsWriteParams{Operation: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "annotations_write")
}

// --- getAnnotations / annotations_read "list" ---

func TestGetAnnotations_UsesCorrectQueryParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/annotations", r.URL.Path)

		q := r.URL.Query()
		assert.Equal(t, "50", q.Get("limit"))
		assert.Equal(t, "dash-1", q.Get("dashboardUID"))
		assert.Equal(t, "true", q.Get("matchAny"))
		assert.Equal(t, "tagA", q["tags"][0])
		assert.Equal(t, "tagB", q["tags"][1])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]interface{}{})
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := getAnnotations(ctx, annotationsReadRequest{
		Limit:        ptr(int64(50)),
		DashboardUID: ptr("dash-1"),
		MatchAny:     ptr(true),
		Tags:         []string{"tagA", "tagB"},
	})
	require.NoError(t, err)
}

func TestGetAnnotations_PropagatesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`oops`))
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := getAnnotations(ctx, annotationsReadRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get annotations:")
}

// --- getAnnotationTags / annotations_read "tags" ---

func TestGetAnnotationTags_UsesCorrectQueryParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/annotations/tags", r.URL.Path)

		q := r.URL.Query()
		assert.Equal(t, "error", q.Get("tag"))
		assert.Equal(t, "50", q.Get("limit"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":{"tags":[]}}`))
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := getAnnotationTags(ctx, annotationsReadRequest{
		Tag:      ptr("error"),
		TagLimit: ptr("50"),
	})
	require.NoError(t, err)
}

// --- createAnnotation / annotations_write "create" ---

func TestCreateAnnotation_GraphiteFormat_Minimal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/annotations/graphite", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "Bearer test", r.Header.Get("Authorization"))

		var body models.PostGraphiteAnnotationsCmd
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "deploy", body.What)
		assert.Equal(t, int64(1710000000000), body.When)
		assert.Nil(t, body.Tags)
		assert.Empty(t, body.Data)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"annotation created"}`))
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := createAnnotation(ctx, AnnotationsWriteParams{
		Format: "graphite",
		What:   "deploy",
		When:   1710000000000,
	})
	require.NoError(t, err)
}

func TestCreateAnnotation_GraphiteFormat_WithTagsAndData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/annotations/graphite", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		assert.Equal(t, "incident", body["what"])
		assert.Equal(t, float64(1720000000000), body["when"])
		assert.ElementsMatch(t, []interface{}{"sev1", "network"}, body["tags"].([]interface{}))
		assert.Equal(t, "context", body["data"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := createAnnotation(ctx, AnnotationsWriteParams{
		Format:       "graphite",
		What:         "incident",
		When:         1720000000000,
		Tags:         []string{"sev1", "network"},
		GraphiteData: "context",
	})
	require.NoError(t, err)
}

func TestCreateAnnotation_SendsCorrectBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/annotations", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var body models.PostAnnotationsCmd
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		assert.Equal(t, int64(7), body.PanelID)
		assert.Equal(t, "hello", *body.Text)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 1}`))
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := createAnnotation(ctx, AnnotationsWriteParams{
		PanelID: 7,
		Text:    ptr("hello"),
	})
	require.NoError(t, err)
}

func TestCreateAnnotation_ErrorWrapped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := createAnnotation(ctx, AnnotationsWriteParams{Text: ptr("t")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create annotation:")
}

func TestCreateAnnotation_GraphiteFormat_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := createAnnotation(ctx, AnnotationsWriteParams{
		Format: "graphite",
		What:   "bad",
		When:   1700000000000,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create graphite annotation")
}

// --- updateAnnotation / annotations_write "update" ---

func TestUpdateAnnotation_UsesPatchMethod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/annotations/"+strconv.Itoa(55), r.URL.Path)
		assert.Equal(t, "PATCH", r.Method)

		var body models.PatchAnnotationsCmd
		_ = json.NewDecoder(r.Body).Decode(&body)

		assert.Equal(t, int64(111), body.Time)
		assert.Equal(t, int64(222), body.TimeEnd)
		assert.Equal(t, "hello", body.Text)
		assert.Equal(t, []string{"a", "b"}, body.Tags)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := updateAnnotation(ctx, AnnotationsWriteParams{
		ID:      55,
		Time:    ptr(int64(111)),
		TimeEnd: ptr(int64(222)),
		Text:    ptr("hello"),
		Tags:    []string{"a", "b"},
	})
	require.NoError(t, err)
}

func TestUpdateAnnotation_SendsOnlyProvidedFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/annotations/"+strconv.Itoa(9), r.URL.Path)
		assert.Equal(t, "PATCH", r.Method)

		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)

		assert.Equal(t, "patched", body["text"])
		assert.ElementsMatch(t, []interface{}{"x"}, body["tags"].([]interface{}))
		assert.Nil(t, body["time"])
		assert.Nil(t, body["timeEnd"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := updateAnnotation(ctx, AnnotationsWriteParams{
		ID:   9,
		Text: ptr("patched"),
		Tags: []string{"x"},
	})
	require.NoError(t, err)
}

// --- deleteAnnotation / annotations_write "delete" ---

func TestDeleteAnnotation_UsesDeleteMethod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/annotations/"+strconv.Itoa(42), r.URL.Path)
		assert.Equal(t, "DELETE", r.Method)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"Annotation deleted"}`))
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	msg, err := deleteAnnotation(ctx, AnnotationsWriteParams{ID: 42})
	require.NoError(t, err)
	assert.Contains(t, msg, "42")
}

func TestDeleteAnnotation_ErrorWrapped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx := mockCtxWithClient(server)

	_, err := deleteAnnotation(ctx, AnnotationsWriteParams{ID: 42})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete annotation")
}

// --- annotations_write end-to-end dispatch, one representative operation per case ---

func TestAnnotationsWriteDispatch_RoutesToTheRightHandler(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	ctx := mockCtxWithClient(server)

	t.Run("create", func(t *testing.T) {
		_, err := annotationsWrite(ctx, AnnotationsWriteParams{Operation: "create", Text: ptr("hi")})
		require.NoError(t, err)
	})
	t.Run("update", func(t *testing.T) {
		_, err := annotationsWrite(ctx, AnnotationsWriteParams{Operation: "update", ID: 1})
		require.NoError(t, err)
	})
	t.Run("delete", func(t *testing.T) {
		_, err := annotationsWrite(ctx, AnnotationsWriteParams{Operation: "delete", ID: 1})
		require.NoError(t, err)
	})
}

func TestAnnotationsReadDispatch_RoutesToTheRightHandler(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/api/annotations/tags" {
			_, _ = w.Write([]byte(`{"result":{"tags":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	ctx := mockCtxWithClient(server)

	t.Run("list", func(t *testing.T) {
		_, err := annotationsRead(ctx, AnnotationsReadParams{Operation: "list"})
		require.NoError(t, err)
	})
	t.Run("tags", func(t *testing.T) {
		_, err := annotationsRead(ctx, AnnotationsReadParams{Operation: "tags"})
		require.NoError(t, err)
	})
}
