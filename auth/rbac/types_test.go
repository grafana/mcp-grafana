package rbac

import "testing"

func TestPermission_String(t *testing.T) {
	p := Permission{Action: "datasources:query", Scope: "datasources:uid:prom"}
	want := "datasources:query @ datasources:uid:prom"
	if got := p.String(); got != want {
		t.Errorf("String()=%q want %q", got, want)
	}
	if got := (Permission{Action: "x"}).String(); got != "x @ *" {
		t.Errorf("empty scope should render as *, got %q", got)
	}
}

func TestToolGate_PublicWhenNoRequirements(t *testing.T) {
	g := ToolGate{}
	if !g.IsPublic() {
		t.Errorf("zero gate should be public")
	}
	g.Permissions = []Permission{{Action: "x"}}
	if g.IsPublic() {
		t.Errorf("gate with permissions is not public")
	}
}
