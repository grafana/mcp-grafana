package mcpgrafana

import (
	"testing"
)

func TestResourceDescriptor_BasePath(t *testing.T) {
	desc := ResourceDescriptor{
		Group:    "dashboard.grafana.app",
		Version:  "v2beta1",
		Resource: "dashboards",
	}

	got := desc.BasePath("default")
	want := "/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards"
	if got != want {
		t.Errorf("BasePath() = %q, want %q", got, want)
	}
}

func TestNewResourceRegistry(t *testing.T) {
	groupList := &APIGroupList{
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
			{
				Name: "folder.grafana.app",
				Versions: []GroupVersionInfo{
					{GroupVersion: "folder.grafana.app/v1beta1", Version: "v1beta1"},
				},
				PreferredVersion: GroupVersionInfo{
					GroupVersion: "folder.grafana.app/v1beta1",
					Version:      "v1beta1",
				},
			},
		},
	}

	reg := NewResourceRegistry(groupList)

	t.Run("HasGroup", func(t *testing.T) {
		if !reg.HasGroup("dashboard.grafana.app") {
			t.Error("expected HasGroup(dashboard.grafana.app) = true")
		}
		if !reg.HasGroup("folder.grafana.app") {
			t.Error("expected HasGroup(folder.grafana.app) = true")
		}
		if reg.HasGroup("nonexistent.grafana.app") {
			t.Error("expected HasGroup(nonexistent.grafana.app) = false")
		}
	})

	t.Run("PreferredVersion", func(t *testing.T) {
		if v := reg.PreferredVersion("dashboard.grafana.app"); v != "v2beta1" {
			t.Errorf("PreferredVersion(dashboard) = %q, want %q", v, "v2beta1")
		}
		if v := reg.PreferredVersion("folder.grafana.app"); v != "v1beta1" {
			t.Errorf("PreferredVersion(folder) = %q, want %q", v, "v1beta1")
		}
		if v := reg.PreferredVersion("nonexistent"); v != "" {
			t.Errorf("PreferredVersion(nonexistent) = %q, want empty", v)
		}
	})

	t.Run("GetGroup", func(t *testing.T) {
		g := reg.GetGroup("dashboard.grafana.app")
		if g == nil {
			t.Fatal("expected non-nil group")
		}
		if g.Name != "dashboard.grafana.app" {
			t.Errorf("Name = %q, want %q", g.Name, "dashboard.grafana.app")
		}
		if len(g.AllVersions) != 2 {
			t.Errorf("AllVersions length = %d, want 2", len(g.AllVersions))
		}
	})

	t.Run("Groups", func(t *testing.T) {
		groups := reg.Groups()
		if len(groups) != 2 {
			t.Errorf("Groups() length = %d, want 2", len(groups))
		}
	})
}

func TestNewResourceRegistry_Nil(t *testing.T) {
	reg := NewResourceRegistry(nil)
	if reg.HasGroup("anything") {
		t.Error("expected empty registry to have no groups")
	}
	if v := reg.PreferredVersion("anything"); v != "" {
		t.Errorf("expected empty preferred version, got %q", v)
	}
	if groups := reg.Groups(); len(groups) != 0 {
		t.Errorf("expected no groups, got %d", len(groups))
	}
}

func TestResourceRegistry_NilReceiver(t *testing.T) {
	var reg *ResourceRegistry
	if reg.HasGroup("anything") {
		t.Error("nil registry should return false for HasGroup")
	}
	if reg.GetGroup("anything") != nil {
		t.Error("nil registry should return nil for GetGroup")
	}
	if v := reg.PreferredVersion("anything"); v != "" {
		t.Errorf("nil registry should return empty PreferredVersion, got %q", v)
	}
	if groups := reg.Groups(); groups != nil {
		t.Errorf("nil registry should return nil for Groups, got %v", groups)
	}
}
