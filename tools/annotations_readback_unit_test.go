//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// annotationServer is a fake annotations API. The write endpoints always
// succeed; readStatus controls the GET by id that the tools use to read the
// stored annotation back.
func annotationServer(t *testing.T, readStatus int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/annotations":
			_, _ = w.Write([]byte(`{"id":7,"message":"Annotation added"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/annotations/7":
			_, _ = w.Write([]byte(`{"message":"Annotation patched"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/annotations/7":
			if readStatus != http.StatusOK {
				w.WriteHeader(readStatus)
				_, _ = w.Write([]byte(`{"message":"denied"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":7,"text":"stored text","time":1700000000000,"tags":["a"]}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func resultJSON(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	return fields
}

func TestCreateAnnotation_ResultKeepsCreateResponseAndAddsStoredAnnotation(t *testing.T) {
	ctx := mockCtxWithClient(annotationServer(t, http.StatusOK))

	resp, err := createAnnotation(ctx, CreateAnnotationInput{Text: "hello"})
	require.NoError(t, err)

	result, ok := resp.(*CreateAnnotationResult)
	require.True(t, ok, "create_annotation returns %T", resp)
	// The create response is where callers have always read the new id.
	require.NotNil(t, result.Payload)
	require.Equal(t, int64(7), *result.Payload.ID)
	require.Equal(t, "Annotation added", *result.Payload.Message)
	// The stored annotation is added next to it.
	require.NotNil(t, result.Annotation)
	require.Equal(t, int64(7), result.Annotation.ID)
	require.Equal(t, "stored text", result.Annotation.Text)
	require.Equal(t, int64(1700000000000), result.Annotation.Time)

	fields := resultJSON(t, resp)
	require.JSONEq(t, `{"id":7,"message":"Annotation added"}`, string(fields["Payload"]))
	require.Contains(t, fields, "annotation")
}

func TestCreateAnnotation_ResultShapeIsTheSameWhenReadBackFails(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctx := mockCtxWithClient(annotationServer(t, status))

			resp, err := createAnnotation(ctx, CreateAnnotationInput{Text: "hello"})
			require.NoError(t, err, "the create succeeded, so a failed read-back must not fail the call")

			result, ok := resp.(*CreateAnnotationResult)
			require.True(t, ok, "create_annotation returns %T", resp)
			require.NotNil(t, result.Payload)
			require.Equal(t, int64(7), *result.Payload.ID)
			require.Nil(t, result.Annotation)

			fields := resultJSON(t, resp)
			require.JSONEq(t, `{"id":7,"message":"Annotation added"}`, string(fields["Payload"]))
			require.NotContains(t, fields, "annotation")
		})
	}
}

func TestUpdateAnnotation_ResultKeepsPatchResponseAndAddsStoredAnnotation(t *testing.T) {
	text := "new text"

	t.Run("read-back available", func(t *testing.T) {
		ctx := mockCtxWithClient(annotationServer(t, http.StatusOK))

		resp, err := updateAnnotation(ctx, UpdateAnnotationInput{ID: 7, Text: &text})
		require.NoError(t, err)

		result, ok := resp.(*UpdateAnnotationResult)
		require.True(t, ok, "update_annotation returns %T", resp)
		require.NotNil(t, result.Payload)
		require.Equal(t, "Annotation patched", result.Payload.Message)
		require.NotNil(t, result.Annotation)
		require.Equal(t, "stored text", result.Annotation.Text)

		fields := resultJSON(t, resp)
		require.JSONEq(t, `{"message":"Annotation patched"}`, string(fields["Payload"]))
		require.Contains(t, fields, "annotation")
	})

	t.Run("read-back denied", func(t *testing.T) {
		ctx := mockCtxWithClient(annotationServer(t, http.StatusForbidden))

		resp, err := updateAnnotation(ctx, UpdateAnnotationInput{ID: 7, Text: &text})
		require.NoError(t, err)

		result, ok := resp.(*UpdateAnnotationResult)
		require.True(t, ok, "update_annotation returns %T", resp)
		require.NotNil(t, result.Payload)
		require.Nil(t, result.Annotation)
		require.NotContains(t, resultJSON(t, resp), "annotation")
	})
}

type ctxMarkerKey struct{}

// contextRecorder is an http.RoundTripper that records, per request, whether
// the request carries the caller's context. Per-call org, extra headers,
// cancellation and tracing all reach the transport through that context.
type contextRecorder struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (c *contextRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.seen[r.Method+" "+r.URL.Path] = r.Context().Value(ctxMarkerKey{}) == "caller"
	c.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

func TestAnnotationReadBack_CarriesRequestContext(t *testing.T) {
	server := annotationServer(t, http.StatusOK)
	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	rec := &contextRecorder{seen: map[string]bool{}}
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.Client = &http.Client{Transport: rec}
	ctx := mcpgrafana.WithGrafanaClient(
		context.WithValue(context.Background(), ctxMarkerKey{}, "caller"),
		&mcpgrafana.GrafanaClient{GrafanaHTTPAPI: client.NewHTTPClientWithConfig(nil, cfg)},
	)

	_, err = createAnnotation(ctx, CreateAnnotationInput{Text: "hello"})
	require.NoError(t, err)
	require.Contains(t, rec.seen, "GET /api/annotations/7", "create must read the annotation back")
	assert.True(t, rec.seen["POST /api/annotations"], "create request lost the caller's context")
	assert.True(t, rec.seen["GET /api/annotations/7"], "read-back after create lost the caller's context")

	rec.seen = map[string]bool{}
	text := "new text"
	_, err = updateAnnotation(ctx, UpdateAnnotationInput{ID: 7, Text: &text})
	require.NoError(t, err)
	require.Contains(t, rec.seen, "GET /api/annotations/7", "update must read the annotation back")
	assert.True(t, rec.seen["PATCH /api/annotations/7"], "update request lost the caller's context")
	assert.True(t, rec.seen["GET /api/annotations/7"], "read-back after update lost the caller's context")
}
