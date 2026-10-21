package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// This file hosts config-generation tools — outputs that operators paste
// into an agent's config, not queries against a datasource. These include
// Alloy and OpenTelemetry label-enforcement snippets; future generators (Mimir,
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
	mcpgrafana.WithTitleAnnotation("Suggest Alloy label enforcement config"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(),
).NotOrgScoped()

// SuggestLokiOTelLabelConfigParams describes resource attribute enforcement for logs.
type SuggestLokiOTelLabelConfigParams struct {
	ApprovedLabels    []string `json:"approvedLabels" jsonschema:"required,description=Resource attribute names to retain using native OTel names such as service.name (not Loki-normalized service_name)."`
	RequiredLabels    []string `json:"requiredLabels,omitempty" jsonschema:"description=Resource attributes to retain and set to unknown when missing or empty."`
	NormalizeLogLevel bool     `json:"normalizeLogLevel,omitempty" jsonschema:"description=Normalize the log record level attribute (not resource attributes or severity_text)."`
	ProcessorName     string   `json:"processorName,omitempty" jsonschema:"description=Collector processor ID: transform or transform/<name>. Defaults to transform/enforce_labels. Names may contain letters and digits plus underscores dots and hyphens."`
}

// SuggestLokiOTelLabelConfigResult is Collector YAML plus integration caveats.
type SuggestLokiOTelLabelConfigResult struct {
	YAML  string   `json:"yaml"`
	Notes []string `json:"notes,omitempty"`
}

var otelLabelProcessorName = regexp.MustCompile(`^transform(/[a-zA-Z0-9_][a-zA-Z0-9_.-]*)?$`)

func suggestLokiOTelLabelConfig(_ context.Context, args SuggestLokiOTelLabelConfigParams) (*SuggestLokiOTelLabelConfigResult, error) {
	if len(args.ApprovedLabels) == 0 {
		return nil, fmt.Errorf("approvedLabels must contain at least one resource attribute")
	}
	processorName := args.ProcessorName
	if processorName == "" {
		processorName = "transform/enforce_labels"
	}
	if !otelLabelProcessorName.MatchString(processorName) {
		return nil, fmt.Errorf("processorName must be transform or transform/<name> with only letters, digits, underscores, dots and hyphens in the name")
	}

	keepSet, requiredSet := map[string]struct{}{}, map[string]struct{}{}
	for _, list := range []struct {
		name   string
		values []string
	}{
		{"approvedLabels", args.ApprovedLabels},
		{"requiredLabels", args.RequiredLabels},
	} {
		for _, name := range list.values {
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("%s must not contain blank resource attribute names", list.name)
			}
			keepSet[name] = struct{}{}
			if list.name == "requiredLabels" {
				requiredSet[name] = struct{}{}
			}
		}
	}
	approved, required := make([]string, 0, len(keepSet)), make([]string, 0, len(requiredSet))
	for name := range keepSet {
		approved = append(approved, name)
	}
	for name := range requiredSet {
		required = append(required, name)
	}
	sort.Strings(approved)
	sort.Strings(required)

	// OTTL strings use Go-style quoting. Escape Collector environment
	// substitutions separately; YAML marshaling alone cannot prevent them.
	quoteAttribute := func(name string) string {
		return strconv.Quote(strings.ReplaceAll(name, "$", "$$"))
	}
	statements := make([]string, 0, len(required)+1)
	for _, name := range required {
		path := "attributes[" + quoteAttribute(name) + "]"
		statements = append(statements, fmt.Sprintf(`set(%s, "unknown") where %s == nil or %s == ""`, path, path, path))
	}
	quoted := make([]string, 0, len(approved))
	for _, name := range approved {
		quoted = append(quoted, quoteAttribute(name))
	}
	statements = append(statements, "keep_keys(attributes, ["+strings.Join(quoted, ", ")+"])")

	type statementGroup struct {
		Context    string   `yaml:"context"`
		Statements []string `yaml:"statements"`
	}
	groups := []statementGroup{{Context: "resource", Statements: statements}}
	if args.NormalizeLogLevel {
		groups = append(groups, statementGroup{
			Context: "log",
			Statements: []string{
				`replace_pattern(attributes["level"], "^(?i)I(nfo)?$", "info") where IsString(attributes["level"])`,
				`replace_pattern(attributes["level"], "^(?i)W(arn(ing)?)?$", "warn") where IsString(attributes["level"])`,
				`replace_pattern(attributes["level"], "^(?i)E(rr(or)?)?$", "error") where IsString(attributes["level"])`,
				`replace_pattern(attributes["level"], "^(?i)D(ebug)?$", "debug") where IsString(attributes["level"])`,
			},
		})
	}
	type processorConfig struct {
		ErrorMode     string           `yaml:"error_mode"`
		LogStatements []statementGroup `yaml:"log_statements"`
	}
	config := struct {
		Processors map[string]processorConfig `yaml:"processors"`
	}{
		Processors: map[string]processorConfig{
			processorName: {ErrorMode: "propagate", LogStatements: groups},
		},
	}
	rendered, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal Collector config: %w", err)
	}
	notes := []string{
		fmt.Sprintf("Requires the OpenTelemetry Collector contrib transform processor. Merge this processors block into your config and add %s to service.pipelines.logs.processors before the OTLP exporter to Loki.", processorName),
		"The resource allowlist removes all unapproved resource attributes, including attributes that would otherwise be retained as structured metadata. It does not configure Loki index labels; log record and scope attributes are not filtered.",
		"Configure Loki limits_config.otlp_config.resource_attributes separately with action: index_label for desired resource attributes; review Loki's default resource index labels and ignore_defaults as needed.",
		"Use native dotted OTel resource attribute names (for example service.name), not Loki-normalized names (service_name).",
		"Required resource attributes are included in the allowlist and receive an unknown placeholder when missing or empty. Shared resource changes affect all log records under that resource.",
	}
	if args.NormalizeLogLevel {
		notes = append(notes, "Normalization applies only to string log record attributes[\"level\"]: I/Info to info, W/Warn/Warning to warn, E/Err/Error to error, and D/Debug to debug (case-insensitive). Other values and severity_text are unchanged; level is not promoted to a resource attribute or index label.")
	} else {
		notes = append(notes, "Consider enabling normalizeLogLevel if log record level attribute values arrive in mixed casing.")
	}
	return &SuggestLokiOTelLabelConfigResult{YAML: string(rendered), Notes: notes}, nil
}

// SuggestLokiOTelLabelConfig is the registered tool wrapper.
var SuggestLokiOTelLabelConfig = mcpgrafana.MustTool(
	"suggest_loki_otel_label_config",
	"Generates OpenTelemetry Collector contrib transform processor YAML for a logs resource attribute allowlist, optional log-level normalization and unknown placeholders. Removes unapproved resource metadata; Loki index labels must be configured separately on the server.",
	suggestLokiOTelLabelConfig,
	mcpgrafana.WithTitleAnnotation("Suggest OpenTelemetry label enforcement config"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(),
).NotOrgScoped()

// AddConfigTools registers the config-generation tool set.
func AddConfigTools(s *mcp.Server) {
	SuggestLokiAlloyLabelConfig.Register(s)
	SuggestLokiOTelLabelConfig.Register(s)
}
