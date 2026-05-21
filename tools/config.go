package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// This file hosts config-generation tools — outputs that operators paste
// into an agent's config, not queries against a datasource. Today there
// is just the Alloy label-enforcement snippet; future generators (Mimir,
// Tempo, etc.) belong here too.

// ---------------------------------------------------------------------------
// suggest_loki_alloy_label_config
// ---------------------------------------------------------------------------

// SuggestLokiAlloyLabelConfigParams describes the desired Alloy pipeline.
type SuggestLokiAlloyLabelConfigParams struct {
	ApprovedLabels    []string `json:"approvedLabels" jsonschema:"required,description=Labels to keep on the index."`
	RequiredLabels    []string `json:"requiredLabels,omitempty" jsonschema:"description=Labels that get an 'unknown' placeholder when missing."`
	NormalizeLogLevel bool     `json:"normalizeLogLevel,omitempty"`
	ComponentName     string   `json:"componentName,omitempty"`
	ForwardTo         string   `json:"forwardTo,omitempty"`
}

// SuggestLokiAlloyLabelConfigResult is the rendered snippet plus notes.
type SuggestLokiAlloyLabelConfigResult struct {
	Alloy string   `json:"alloy"`
	Notes []string `json:"notes,omitempty"`
}

func suggestLokiAlloyLabelConfig(_ context.Context, args SuggestLokiAlloyLabelConfigParams) (*SuggestLokiAlloyLabelConfigResult, error) {
	if len(args.ApprovedLabels) == 0 {
		return nil, fmt.Errorf("approvedLabels must contain at least one label")
	}

	componentName := args.ComponentName
	if componentName == "" {
		componentName = "enforce_labels"
	}
	forwardTo := args.ForwardTo
	if forwardTo == "" {
		forwardTo = "loki.write.default.receiver"
	}

	// Union RequiredLabels into the kept set. Without this, a required
	// label that isn't also in ApprovedLabels would get an "unknown"
	// placeholder injected, then immediately dropped by stage.label_keep.
	keepSet := make(map[string]struct{}, len(args.ApprovedLabels)+len(args.RequiredLabels))
	for _, l := range args.ApprovedLabels {
		keepSet[l] = struct{}{}
	}
	for _, l := range args.RequiredLabels {
		keepSet[l] = struct{}{}
	}
	approved := make([]string, 0, len(keepSet))
	for l := range keepSet {
		approved = append(approved, l)
	}
	sort.Strings(approved)

	var b strings.Builder
	fmt.Fprintf(&b, "loki.process %q {\n", componentName)
	fmt.Fprintf(&b, "  forward_to = [%s]\n\n", forwardTo)

	if args.NormalizeLogLevel {
		b.WriteString("  // Normalize log level: I/Info/INFO -> info, etc. Patterns are anchored so partial matches\n")
		b.WriteString("  // inside other level values (e.g. \"CRITICAL\", \"trace\") aren't mangled.\n")
		b.WriteString("  stage.replace {\n    source = \"level\"\n    expression = \"^(?i)I(nfo)?$\"\n    replace = \"info\"\n  }\n")
		b.WriteString("  stage.replace {\n    source = \"level\"\n    expression = \"^(?i)W(arn(ing)?)?$\"\n    replace = \"warn\"\n  }\n")
		b.WriteString("  stage.replace {\n    source = \"level\"\n    expression = \"^(?i)E(rr(or)?)?$\"\n    replace = \"error\"\n  }\n")
		b.WriteString("  stage.replace {\n    source = \"level\"\n    expression = \"^(?i)D(ebug)?$\"\n    replace = \"debug\"\n  }\n")
		b.WriteString("  stage.labels {\n    values = { level = \"\" }\n  }\n\n")
	}

	for _, name := range args.RequiredLabels {
		fmt.Fprintf(&b, "  // Soft enforcement: inject \"unknown\" when %q is missing so violations are queryable.\n", name)
		fmt.Fprintf(&b, "  stage.template {\n    source = %q\n    template = \"{{ if .Value }}{{ .Value }}{{ else }}unknown{{ end }}\"\n  }\n", name)
		fmt.Fprintf(&b, "  stage.labels {\n    values = { %s = \"\" }\n  }\n\n", name)
	}

	b.WriteString("  // Final enforcement: keep only the approved label set.\n")
	b.WriteString("  stage.label_keep {\n    values = [")
	for i, label := range approved {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", label)
	}
	b.WriteString("]\n  }\n")
	b.WriteString("}\n")

	notes := []string{
		"stage.label_keep is the last enforcement step — anything not listed in approvedLabels (or requiredLabels) is dropped.",
	}
	if !args.NormalizeLogLevel {
		notes = append(notes, "Consider enabling normalizeLogLevel if level values arrive in mixed casing.")
	}
	if len(args.RequiredLabels) > 0 {
		notes = append(notes, "Soft enforcement is a runway to hard enforcement; once `unknown` counts trend to zero, switch to dropping signals with missing required labels.")
	}

	return &SuggestLokiAlloyLabelConfigResult{
		Alloy: b.String(),
		Notes: notes,
	}, nil
}

// SuggestLokiAlloyLabelConfig is the registered tool wrapper.
var SuggestLokiAlloyLabelConfig = mcpgrafana.MustTool(
	"suggest_loki_alloy_label_config",
	"Generates an Alloy loki.process snippet enforcing an approved label set via stage.label_keep, with optional log-level normalisation and soft-enforcement placeholders.",
	suggestLokiAlloyLabelConfig,
	mcp.WithTitleAnnotation("Suggest Alloy label enforcement config"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddConfigTools registers the config-generation tool set.
func AddConfigTools(s *server.MCPServer) {
	SuggestLokiAlloyLabelConfig.Register(s)
}
