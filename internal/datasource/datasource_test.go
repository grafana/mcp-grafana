//go:build unit

package datasource

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestByUID_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Data source not found"}`))
	}))
	defer server.Close()

	_, err := ByUID(mockDatasourcesCtx(server), "missing")
	require.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, "datasource with UID 'missing' not found. Please check if the datasource exists and is accessible", err.Error())
}

// Only a 404 status means not found: other failures, even ones whose text
// mentions 404, go through the frontend-settings fallback.
func TestByUID_ServerErrorMentioning404IsNotNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"upstream returned 404"}`))
	}))
	defer server.Close()

	_, err := ByUID(mockDatasourcesCtx(server), "prom")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNotFound), "got %v", err)
}

// When the metadata API is inaccessible, the fallback's not-found also
// matches ErrNotFound.
func TestFallbackNotFoundMatchesErrNotFound(t *testing.T) {
	server := newFallbackTestServer()
	defer server.Close()
	ctx := mockDatasourcesCtx(server)

	_, err := ByUID(ctx, "nope")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = ByName(ctx, "nope")
	assert.ErrorIs(t, err, ErrNotFound)
}
