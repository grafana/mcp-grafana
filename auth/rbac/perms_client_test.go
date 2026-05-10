package rbac

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchPermissions_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/access-control/user/permissions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer abc" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string][]string{
			"datasources:read":  {"datasources:*"},
			"datasources:query": {"datasources:uid:prom-prod", "datasources:uid:loki-prod"},
		})
	}))
	defer srv.Close()

	pc := NewPermsClient(srv.URL, srv.Client())
	got, err := pc.Fetch(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got["datasources:query"]) != 2 {
		t.Errorf("got %+v", got)
	}
}

func TestFetchPermissions_NotEnterpriseReturnsEmpty(t *testing.T) {
	// Grafana OSS responds with 200 + the user's built-in role permissions.
	// An empty body is also valid (e.g., when fine-grained RBAC isn't enabled).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	pc := NewPermsClient(srv.URL, srv.Client())
	got, err := pc.Fetch(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

func TestFetchPermissions_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	pc := NewPermsClient(srv.URL, srv.Client())
	if _, err := pc.Fetch(context.Background(), "abc"); err == nil {
		t.Errorf("expected error on 500")
	}
}
