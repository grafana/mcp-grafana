package mcpgrafana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverCapabilities_LegacyGrafana(t *testing.T) {
	// Legacy Grafana returns 404 for /apis.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis" {
			http.NotFound(w, r)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	caps, err := client.DiscoverCapabilities(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps.HasKubernetesAPIs {
		t.Fatal("expected HasKubernetesAPIs to be false for legacy Grafana")
	}
	if caps.Registry != nil {
		t.Fatal("expected Registry to be nil for legacy Grafana")
	}
}

func TestDiscoverCapabilities_K8sGrafana(t *testing.T) {
	groupList := APIGroupList{
		Kind: "APIGroupList",
		Groups: []APIGroup{
			{
				Name: "dashboard.grafana.app",
				Versions: []GroupVersionInfo{
					{GroupVersion: "dashboard.grafana.app/v0alpha1", Version: "v0alpha1"},
					{GroupVersion: "dashboard.grafana.app/v2beta1", Version: "v2beta1"},
				},
				PreferredVersion: GroupVersionInfo{
					GroupVersion: "dashboard.grafana.app/v0alpha1",
					Version:      "v0alpha1",
				},
			},
			{
				Name: "folder.grafana.app",
				Versions: []GroupVersionInfo{
					{GroupVersion: "folder.grafana.app/v0alpha1", Version: "v0alpha1"},
				},
				PreferredVersion: GroupVersionInfo{
					GroupVersion: "folder.grafana.app/v0alpha1",
					Version:      "v0alpha1",
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apis" {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(groupList); err != nil {
				t.Fatalf("failed to encode response: %v", err)
			}
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	caps, err := client.DiscoverCapabilities(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !caps.HasKubernetesAPIs {
		t.Fatal("expected HasKubernetesAPIs to be true")
	}
	if caps.Registry == nil {
		t.Fatal("expected Registry to be non-nil")
	}
	if !caps.Registry.HasGroup("dashboard.grafana.app") {
		t.Fatal("expected dashboard.grafana.app group to be present")
	}
	if !caps.Registry.HasGroup("folder.grafana.app") {
		t.Fatal("expected folder.grafana.app group to be present")
	}
	if v := caps.Registry.PreferredVersion("dashboard.grafana.app"); v != "v0alpha1" {
		t.Fatalf("expected preferred version v0alpha1, got %s", v)
	}
}

func TestDiscoverCapabilities_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	client := &KubernetesClient{BaseURL: srv.URL, HTTPClient: srv.Client()}
	_, err := client.DiscoverCapabilities(context.Background())
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestShouldUseKubernetesAPI(t *testing.T) {
	tests := []struct {
		name     string
		caps     *GrafanaCapabilities
		apiGroup string
		want     bool
	}{
		{
			name:     "nil capabilities",
			caps:     nil,
			apiGroup: "dashboard.grafana.app",
			want:     false,
		},
		{
			name:     "legacy grafana",
			caps:     &GrafanaCapabilities{HasKubernetesAPIs: false},
			apiGroup: "dashboard.grafana.app",
			want:     false,
		},
		{
			name: "k8s grafana with matching group",
			caps: &GrafanaCapabilities{
				HasKubernetesAPIs: true,
				Registry: NewResourceRegistry(&APIGroupList{
					Groups: []APIGroup{
						{
							Name: "dashboard.grafana.app",
							Versions: []GroupVersionInfo{
								{GroupVersion: "dashboard.grafana.app/v0alpha1", Version: "v0alpha1"},
							},
							PreferredVersion: GroupVersionInfo{Version: "v0alpha1"},
						},
					},
				}),
			},
			apiGroup: "dashboard.grafana.app",
			want:     true,
		},
		{
			name: "k8s grafana without matching group",
			caps: &GrafanaCapabilities{
				HasKubernetesAPIs: true,
				Registry: NewResourceRegistry(&APIGroupList{
					Groups: []APIGroup{
						{
							Name: "folder.grafana.app",
							Versions: []GroupVersionInfo{
								{GroupVersion: "folder.grafana.app/v0alpha1", Version: "v0alpha1"},
							},
							PreferredVersion: GroupVersionInfo{Version: "v0alpha1"},
						},
					},
				}),
			},
			apiGroup: "dashboard.grafana.app",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.caps.ShouldUseKubernetesAPI(tt.apiGroup)
			if got != tt.want {
				t.Fatalf("ShouldUseKubernetesAPI(%q) = %v, want %v", tt.apiGroup, got, tt.want)
			}
		})
	}
}
