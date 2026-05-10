package rbac

import (
	"fmt"
	"strings"
)

// Mode controls how the gate filters tools.
type Mode string

const (
	// ModeAuto resolves to ModeEnterprise or ModeBasic on first permission
	// fetch, depending on whether the response is non-empty.
	ModeAuto Mode = "auto"
	// ModeEnterprise uses fine-grained RBAC permissions.
	ModeEnterprise Mode = "enterprise"
	// ModeBasic uses BasicRole (Viewer/Editor/Admin) only.
	ModeBasic Mode = "basic"
	// ModeOff disables tool filtering entirely (every authenticated user
	// sees the full tool list).
	ModeOff Mode = "off"
)

// ParseMode normalizes a CLI flag value into a Mode.
func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case "", ModeAuto:
		return ModeAuto, nil
	case ModeEnterprise:
		return ModeEnterprise, nil
	case ModeBasic:
		return ModeBasic, nil
	case ModeOff:
		return ModeOff, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrModeUnknown, s)
	}
}

// ResolveAuto picks Enterprise or Basic based on whether the permission set
// is non-empty. Used at first permission fetch when Mode is ModeAuto.
func ResolveAuto(perms PermissionSet) Mode {
	if len(perms) > 0 {
		return ModeEnterprise
	}
	return ModeBasic
}
