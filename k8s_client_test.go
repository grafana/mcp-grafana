package mcpgrafana

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

// newTestServer creates an httptest.Server that routes requests based on path.
func newTestServer(t *testing.T, routes map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"kind":    "Status",
				"status":  "Failure",
				"message": "not found",
				"code":    404,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func testAPIGroupList() APIGroupList {
	return APIGroupList{
		Kind: "APIGroupList",
		Groups: []APIGroup{
			{
				Name: "dashboard.grafana.app",
				Versions: []GroupVersionInfo{
					{GroupVersion: "dashboard.grafana.app/v2beta1", Version: "v2beta1"},
					{GroupVersion: "dashboard.grafana.app/v1beta1", Version: "v1beta1"},
				},
				PreferredVersion: GroupVersionInfo{
					GroupVersion: "dashboard.grafana.app/v2beta1",
					Version:      "v2beta1",
				},
			},
		},
	}
}

var testDashboardDesc = ResourceDescriptor{
	Group:    "dashboard.grafana.app",
	Version:  "v2beta1",
	Resource: "dashboards",
}

func TestKubernetesClient_Discover(t *testing.T) {
	groupList := testAPIGroupList()
	ts := newTestServer(t, map[string]interface{}{
		"/apis": groupList,
	})
	defer ts.Close()

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	reg, err := client.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error: %v", err)
	}

	if !reg.HasGroup("dashboard.grafana.app") {
		t.Error("expected registry to have dashboard.grafana.app")
	}
	if v := reg.PreferredVersion("dashboard.grafana.app"); v != "v2beta1" {
		t.Errorf("preferred version = %q, want %q", v, "v2beta1")
	}
}

func TestKubernetesClient_Get(t *testing.T) {
	dashObj := map[string]interface{}{
		"kind":       "Dashboard",
		"apiVersion": "dashboard.grafana.app/v2beta1",
		"metadata": map[string]interface{}{
			"name":      "my-dashboard",
			"namespace": "default",
		},
		"spec": map[string]interface{}{
			"title": "My Dashboard",
		},
	}

	ts := newTestServer(t, map[string]interface{}{
		"/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/my-dashboard": dashObj,
	})
	defer ts.Close()

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	result, err := client.Get(context.Background(), testDashboardDesc, "default", "my-dashboard")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	spec, ok := result["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("expected spec to be a map")
	}
	if title := spec["title"]; title != "My Dashboard" {
		t.Errorf("title = %v, want %q", title, "My Dashboard")
	}
}

func TestKubernetesClient_Get_NotFound(t *testing.T) {
	ts := newTestServer(t, map[string]interface{}{})
	defer ts.Close()

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	_, err := client.Get(context.Background(), testDashboardDesc, "default", "nonexistent")
	if err == nil {
		t.Fatal("expected error for 404")
	}

	apiErr, ok := err.(*KubernetesAPIError)
	if !ok {
		t.Fatalf("expected *KubernetesAPIError, got %T", err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestKubernetesClient_List(t *testing.T) {
	listResp := map[string]interface{}{
		"kind":       "DashboardList",
		"apiVersion": "dashboard.grafana.app/v2beta1",
		"items": []interface{}{
			map[string]interface{}{
				"kind": "Dashboard",
				"metadata": map[string]interface{}{
					"name":      "dash-1",
					"namespace": "default",
				},
			},
			map[string]interface{}{
				"kind": "Dashboard",
				"metadata": map[string]interface{}{
					"name":      "dash-2",
					"namespace": "default",
				},
			},
		},
		"metadata": map[string]interface{}{
			"resourceVersion": "1234",
		},
	}

	ts := newTestServer(t, map[string]interface{}{
		"/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards": listResp,
	})
	defer ts.Close()

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	result, err := client.List(context.Background(), testDashboardDesc, "default", nil)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("Items length = %d, want 2", len(result.Items))
	}
	if result.Kind != "DashboardList" {
		t.Errorf("Kind = %q, want %q", result.Kind, "DashboardList")
	}
}

func TestKubernetesClient_List_WithOptions(t *testing.T) {
	listResp := map[string]interface{}{
		"kind":       "DashboardList",
		"apiVersion": "dashboard.grafana.app/v2beta1",
		"items":      []interface{}{},
	}

	var capturedQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listResp)
	}))
	defer ts.Close()

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	opts := &ListOptions{
		LabelSelector: "app=foo,env=prod",
		Limit:         50,
		Continue:      "eyJjb250aW51ZSI6InRlc3QifQ==",
	}
	_, err := client.List(context.Background(), testDashboardDesc, "default", opts)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	if got := capturedQuery.Get("labelSelector"); got != "app=foo,env=prod" {
		t.Errorf("labelSelector = %q, want %q", got, "app=foo,env=prod")
	}
	if got := capturedQuery.Get("limit"); got != "50" {
		t.Errorf("limit = %q, want %q", got, "50")
	}
	if got := capturedQuery.Get("continue"); got != "eyJjb250aW51ZSI6InRlc3QifQ==" {
		t.Errorf("continue = %q, want %q", got, "eyJjb250aW51ZSI6InRlc3QifQ==")
	}
}

func TestKubernetesClient_List_WithSpecialCharacters(t *testing.T) {
	listResp := map[string]interface{}{
		"kind":       "DashboardList",
		"apiVersion": "dashboard.grafana.app/v2beta1",
		"items":      []interface{}{},
	}

	var capturedQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listResp)
	}))
	defer ts.Close()

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	// Label selector with special characters that need URL encoding.
	opts := &ListOptions{
		LabelSelector: "app in (foo,bar),version!=v1&test",
	}
	_, err := client.List(context.Background(), testDashboardDesc, "default", opts)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	// The server should receive the properly decoded value.
	if got := capturedQuery.Get("labelSelector"); got != "app in (foo,bar),version!=v1&test" {
		t.Errorf("labelSelector = %q, want %q", got, "app in (foo,bar),version!=v1&test")
	}
}

func TestKubernetesClient_List_WithNamespace(t *testing.T) {
	listResp := map[string]interface{}{
		"kind":       "DashboardList",
		"apiVersion": "dashboard.grafana.app/v2beta1",
		"items":      []interface{}{},
	}

	var capturedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listResp)
	}))
	defer ts.Close()

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	_, err := client.List(context.Background(), testDashboardDesc, "my-org", nil)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	wantPath := "/apis/dashboard.grafana.app/v2beta1/namespaces/my-org/dashboards"
	if capturedPath != wantPath {
		t.Errorf("request path = %q, want %q", capturedPath, wantPath)
	}
}

func TestKubernetesClient_PathTraversal(t *testing.T) {
	ts := newTestServer(t, map[string]interface{}{})
	defer ts.Close()

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	t.Run("Get rejects slash in namespace", func(t *testing.T) {
		_, err := client.Get(context.Background(), testDashboardDesc, "default/../../etc", "my-dash")
		if err == nil {
			t.Fatal("expected error for path traversal in namespace")
		}
		if got := err.Error(); got == "" {
			t.Fatal("expected non-empty error message")
		}
	})

	t.Run("Get rejects slash in name", func(t *testing.T) {
		_, err := client.Get(context.Background(), testDashboardDesc, "default", "../../etc/passwd")
		if err == nil {
			t.Fatal("expected error for path traversal in name")
		}
	})

	t.Run("Get rejects backslash in namespace", func(t *testing.T) {
		_, err := client.Get(context.Background(), testDashboardDesc, `default\..`, "my-dash")
		if err == nil {
			t.Fatal("expected error for backslash in namespace")
		}
	})

	t.Run("List rejects slash in namespace", func(t *testing.T) {
		_, err := client.List(context.Background(), testDashboardDesc, "default/../../etc", nil)
		if err == nil {
			t.Fatal("expected error for path traversal in namespace")
		}
	})

	t.Run("Get allows valid names", func(t *testing.T) {
		// This will 404 but should not return a validation error.
		_, err := client.Get(context.Background(), testDashboardDesc, "default", "my-dashboard")
		if err == nil {
			t.Fatal("expected 404 error")
		}
		apiErr, ok := err.(*KubernetesAPIError)
		if !ok {
			t.Fatalf("expected *KubernetesAPIError, got %T: %v", err, err)
		}
		if apiErr.StatusCode != 404 {
			t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
		}
	})
}

func TestKubernetesClient_AuthAPIKey(t *testing.T) {
	var capturedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind": "Dashboard",
			"metadata": map[string]interface{}{
				"name": "test",
			},
		})
	}))
	defer ts.Close()

	cfg := GrafanaConfig{APIKey: "my-secret-token"}
	rt, err := BuildTransport(&cfg, nil)
	if err != nil {
		t.Fatalf("BuildTransport() error: %v", err)
	}

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: &http.Client{Transport: rt},
	}

	_, err = client.Get(context.Background(), testDashboardDesc, "default", "test")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	want := "Bearer my-secret-token"
	if capturedAuth != want {
		t.Errorf("Authorization = %q, want %q", capturedAuth, want)
	}
}

func TestKubernetesClient_AuthOnBehalfOf(t *testing.T) {
	var capturedAccessToken, capturedIDToken string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAccessToken = r.Header.Get("X-Access-Token")
		capturedIDToken = r.Header.Get("X-Grafana-Id")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind": "Dashboard",
			"metadata": map[string]interface{}{
				"name": "test",
			},
		})
	}))
	defer ts.Close()

	cfg := GrafanaConfig{
		AccessToken: "access-token-123",
		IDToken:     "id-token-456",
		APIKey:      "should-not-be-used",
	}
	rt, err := BuildTransport(&cfg, nil)
	if err != nil {
		t.Fatalf("BuildTransport() error: %v", err)
	}

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: &http.Client{Transport: rt},
	}

	_, err = client.Get(context.Background(), testDashboardDesc, "default", "test")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	if capturedAccessToken != "access-token-123" {
		t.Errorf("X-Access-Token = %q, want %q", capturedAccessToken, "access-token-123")
	}
	if capturedIDToken != "id-token-456" {
		t.Errorf("X-Grafana-Id = %q, want %q", capturedIDToken, "id-token-456")
	}
}

func TestKubernetesClient_AuthBasicAuth(t *testing.T) {
	var capturedUser, capturedPass string
	var basicAuthOK bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUser, capturedPass, basicAuthOK = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind": "Dashboard",
			"metadata": map[string]interface{}{
				"name": "test",
			},
		})
	}))
	defer ts.Close()

	cfg := GrafanaConfig{BasicAuth: url.UserPassword("admin", "secret")}
	rt, err := BuildTransport(&cfg, nil)
	if err != nil {
		t.Fatalf("BuildTransport() error: %v", err)
	}

	client := &KubernetesClient{
		BaseURL:    ts.URL,
		HTTPClient: &http.Client{Transport: rt},
	}

	_, err = client.Get(context.Background(), testDashboardDesc, "default", "test")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	if !basicAuthOK {
		t.Fatal("expected basic auth to be set")
	}
	if capturedUser != "admin" {
		t.Errorf("username = %q, want %q", capturedUser, "admin")
	}
	if capturedPass != "secret" {
		t.Errorf("password = %q, want %q", capturedPass, "secret")
	}
}

func TestKubernetesClient_Create(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":     "Dashboard",
			"metadata": map[string]interface{}{"name": "new-dash", "generation": 1},
		})
	}))
	defer ts.Close()

	client := &KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	obj := map[string]interface{}{
		"apiVersion": "dashboard.grafana.app/v2beta1",
		"kind":       "Dashboard",
		"spec":       map[string]interface{}{"title": "New"},
	}
	result, err := client.Create(context.Background(), testDashboardDesc, "default", obj)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if !json.Valid([]byte(gotBody)) || gotBody == "" {
		t.Errorf("request body should be JSON, got %q", gotBody)
	}
	if md, _ := result["metadata"].(map[string]interface{}); md["name"] != "new-dash" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestKubernetesClient_Update(t *testing.T) {
	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":     "Dashboard",
			"metadata": map[string]interface{}{"name": "my-dash", "generation": 5},
		})
	}))
	defer ts.Close()

	client := &KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	obj := map[string]interface{}{
		"apiVersion": "dashboard.grafana.app/v2beta1",
		"kind":       "Dashboard",
		"metadata":   map[string]interface{}{"name": "my-dash", "resourceVersion": "42"},
		"spec":       map[string]interface{}{"title": "Updated"},
	}
	if _, err := client.Update(context.Background(), testDashboardDesc, "default", "my-dash", obj); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if want := "/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards/my-dash"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestKubernetesClient_GroupVersions(t *testing.T) {
	const group = "dashboard.grafana.app"

	t.Run("served group returns versions and caches them", func(t *testing.T) {
		var calls int32
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/apis/"+group {
				atomic.AddInt32(&calls, 1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"versions": []map[string]interface{}{{"version": "v0alpha1"}, {"version": "v1beta1"}, {"version": "v2beta1"}},
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		client := &KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}

		versions, err := client.GroupVersions(context.Background(), group)
		if err != nil {
			t.Fatalf("GroupVersions() error: %v", err)
		}
		if len(versions) != 3 {
			t.Errorf("versions = %v, want 3", versions)
		}
		if !client.SupportsGroupVersion(context.Background(), group, "v1beta1") {
			t.Error("expected v1beta1 to be supported")
		}
		if client.SupportsGroupVersion(context.Background(), group, "v1") {
			t.Error("did not expect v1 to be supported")
		}
		// Repeated calls are served from cache (one HTTP request total).
		_, _ = client.GroupVersions(context.Background(), group)
		if got := atomic.LoadInt32(&calls); got != 1 {
			t.Errorf("discovery calls = %d, want 1 (cached)", got)
		}
	})

	t.Run("absent group returns empty and caches it", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		client := &KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
		versions, err := client.GroupVersions(context.Background(), group)
		if err != nil {
			t.Fatalf("GroupVersions() error: %v", err)
		}
		if len(versions) != 0 {
			t.Errorf("versions = %v, want empty for absent group", versions)
		}
		if client.SupportsGroupVersion(context.Background(), group, "v1beta1") {
			t.Error("absent group must not support any version")
		}
	})

	t.Run("transient error is returned and not cached", func(t *testing.T) {
		var calls int32
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		client := &KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
		if _, err := client.GroupVersions(context.Background(), group); err == nil {
			t.Fatal("expected an error on a 5xx discovery response")
		}
		// A transient failure must not be cached, so a second call re-probes.
		_, _ = client.GroupVersions(context.Background(), group)
		if got := atomic.LoadInt32(&calls); got != 2 {
			t.Errorf("discovery calls = %d, want 2 (transient errors not cached)", got)
		}
	})
}

func TestKubernetesClient_ErrorMessage(t *testing.T) {
	apiErr := &KubernetesAPIError{
		StatusCode: 404,
		Status:     "404 Not Found",
		Body:       `{"message":"not found"}`,
	}

	msg := apiErr.Error()
	if msg == "" {
		t.Error("expected non-empty error message")
	}
}
