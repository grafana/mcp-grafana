package mcpgrafana

import (
	"fmt"
	"sort"
	"strings"
)

// unknownArguments returns the top-level argument keys that do not match any
// schema property, so that a typo'd argument is rejected instead of silently
// dropped by the decoder. Matching is case-insensitive, mirroring
// encoding/json's fallback field matching. Non-object argument shapes are left
// to the decode path to report.
func unknownArguments(args any, properties map[string]any) []string {
	argMap, ok := args.(map[string]any)
	if !ok {
		return nil
	}
	var unknown []string
	for key := range argMap {
		if _, ok := properties[key]; ok {
			continue
		}
		if !hasKeyFold(properties, key) {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// hasKeyFold reports whether m contains a key equal to k under Unicode case
// folding.
func hasKeyFold(m map[string]any, k string) bool {
	for name := range m {
		if strings.EqualFold(name, k) {
			return true
		}
	}
	return false
}

// unknownArgumentsError builds the error message for a call carrying unknown
// arguments. Listing the valid argument names makes the error self-correcting
// for LLM callers.
func unknownArgumentsError(unknown []string, properties map[string]any) string {
	var sb strings.Builder
	sb.WriteString("unknown argument")
	if len(unknown) > 1 {
		sb.WriteString("s")
	}
	for i, key := range unknown {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, " %q", key)
	}
	if len(properties) > 0 {
		known := make([]string, 0, len(properties))
		for name := range properties {
			known = append(known, name)
		}
		sort.Strings(known)
		fmt.Fprintf(&sb, "; valid arguments: %s", strings.Join(known, ", "))
	} else {
		sb.WriteString("; this tool takes no arguments")
	}
	return sb.String()
}
