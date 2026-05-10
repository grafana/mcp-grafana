package rbac

import "testing"

func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Mode
		err  bool
	}{
		{"", ModeAuto, false},
		{"auto", ModeAuto, false},
		{"AUTO", ModeAuto, false},
		{"enterprise", ModeEnterprise, false},
		{"basic", ModeBasic, false},
		{"off", ModeOff, false},
		{"bogus", "", true},
	} {
		got, err := ParseMode(tc.in)
		if (err != nil) != tc.err {
			t.Errorf("ParseMode(%q) err=%v wantErr=%v", tc.in, err, tc.err)
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveAutoMode_EnterpriseWhenPermsNonEmpty(t *testing.T) {
	got := ResolveAuto(PermissionSet{"datasources:read": []string{"datasources:*"}})
	if got != ModeEnterprise {
		t.Errorf("non-empty perms should resolve to enterprise, got %q", got)
	}
}

func TestResolveAutoMode_BasicWhenPermsEmpty(t *testing.T) {
	got := ResolveAuto(PermissionSet{})
	if got != ModeBasic {
		t.Errorf("empty perms should resolve to basic, got %q", got)
	}
}
