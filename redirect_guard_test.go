package mcpgrafana

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBuildTransportRejectsCrossOriginRedirect(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		method     string
		config     GrafanaConfig
		authHeader string
		authValue  string
	}{
		{name: "302 bearer", status: http.StatusFound, method: http.MethodGet, config: GrafanaConfig{APIKey: "grafana-token"}, authHeader: "Authorization", authValue: "Bearer grafana-token"},
		{name: "307 bearer", status: http.StatusTemporaryRedirect, method: http.MethodPost, config: GrafanaConfig{APIKey: "grafana-token"}, authHeader: "Authorization", authValue: "Bearer grafana-token"},
		{name: "302 OBO", status: http.StatusFound, method: http.MethodGet, config: GrafanaConfig{AccessToken: "grafana-token", IDToken: "grafana-id"}, authHeader: "X-Access-Token", authValue: "grafana-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var destinationRequests atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				destinationRequests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer destination.Close()

			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(tc.authHeader); got != tc.authValue {
					t.Errorf("source %s = %q", tc.authHeader, got)
				}
				http.Redirect(w, r, destination.URL+"/capture", tc.status)
			}))
			defer source.Close()

			transport, err := BuildTransport(&tc.config, nil)
			if err != nil {
				t.Fatal(err)
			}
			var body io.Reader
			if tc.method == http.MethodPost {
				body = strings.NewReader("request body")
			}
			req, err := http.NewRequest(tc.method, source.URL+"/api/ds/query", body)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := (&http.Client{Transport: transport}).Do(req)
			if resp != nil {
				resp.Body.Close()
				t.Fatalf("unexpected response: %s", resp.Status)
			}
			if !errors.Is(err, errCrossOriginRedirect) {
				t.Fatalf("redirect error = %v, want %v", err, errCrossOriginRedirect)
			}
			if got := destinationRequests.Load(); got != 0 {
				t.Fatalf("destination received %d requests", got)
			}
		})
	}
}

func TestBuildTransportAllowsSameOriginRedirect(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer grafana-token" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/finish", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	transport, err := BuildTransport(&GrafanaConfig{APIKey: "grafana-token"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: transport}).Get(server.URL + "/start")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || requests.Load() != 2 {
		t.Fatalf("status = %d, requests = %d", resp.StatusCode, requests.Load())
	}
}

func TestBuildTransportAllowsCrossOriginRedirectWhenConfigured(t *testing.T) {
	var receivedAuth atomic.Value
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	transport, err := BuildTransport(&GrafanaConfig{
		APIKey:                    "grafana-token",
		AllowCrossOriginRedirects: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: transport}).Get(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := receivedAuth.Load(); resp.StatusCode != http.StatusNoContent || got != "Bearer grafana-token" {
		t.Fatalf("status = %d, destination Authorization = %q", resp.StatusCode, got)
	}
}
