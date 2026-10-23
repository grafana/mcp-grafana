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
	ApprovedLabels     []string `json:"approvedLabels" jsonschema:"required,description=Intended index labels using native OTel resource names such as service.name. Does not change Loki indexing."`
	RequiredLabels     []string `json:"requiredLabels,omitempty" jsonschema:"description=Resource attributes to retain and set to unknown when missing or empty."`
	LokiIndexLabels    []string `json:"lokiIndexLabels,omitempty" jsonschema:"description=Effective Loki resource index attributes for guidance only. Replaces the assumed defaults; an explicit empty list means none."`
	DropAttributes     []string `json:"dropAttributes,omitempty" jsonschema:"description=Resource attributes to delete. Existing log and scope attributes are unaffected."`
	MetadataAttributes []string `json:"metadataAttributes,omitempty" jsonschema:"description=Resource attributes to move to log attributes before removing the resource originals. Existing log values win."`
	OtherAttributes    string   `json:"otherAttributes,omitempty" jsonschema:"enum=keep,enum=metadata,enum=drop,description=Treatment of resource attributes outside approvedLabels and requiredLabels: keep (default) or move to metadata or drop. Explicit actions take precedence."`
	NormalizeLogLevel  bool     `json:"normalizeLogLevel,omitempty" jsonschema:"description=Normalize the log record level attribute (not resource attributes or severity_text)."`
	ProcessorName      string   `json:"processorName,omitempty" jsonschema:"description=Collector processor ID: transform or transform/<name>. Defaults to transform/enforce_labels. Names may contain letters and digits plus underscores dots and hyphens."`
}

// SuggestLokiOTelLabelConfigResult is Collector YAML plus integration caveats.
type SuggestLokiOTelLabelConfigResult struct {
	YAML  string   `json:"yaml"`
	Notes []string `json:"notes,omitempty"`
}

var otelLabelProcessorName = regexp.MustCompile(`^transform(/[a-zA-Z0-9_][a-zA-Z0-9_.-]*)?$`)

var lokiDefaultOTelIndexLabels = []string{
	"service.name", "service.namespace", "service.instance.id",
	"deployment.environment", "deployment.environment.name",
	"cloud.region", "cloud.availability_zone", "k8s.cluster.name",
	"k8s.namespace.name", "k8s.pod.name", "k8s.container.name", "container.name",
	"k8s.replicaset.name", "k8s.deployment.name", "k8s.statefulset.name",
	"k8s.daemonset.name", "k8s.cronjob.name", "k8s.job.name",
}

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

	mode := args.OtherAttributes
	if mode == "" {
		mode = "keep"
	}
	if mode != "keep" && mode != "metadata" && mode != "drop" {
		return nil, fmt.Errorf("otherAttributes must be keep, metadata or drop")
	}
	keepSet, requiredSet := map[string]struct{}{}, map[string]struct{}{}
	metadataSet, dropSet, indexSet := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	indexLabels := args.LokiIndexLabels
	if indexLabels == nil {
		indexLabels = lokiDefaultOTelIndexLabels
	}
	for _, list := range []struct {
		name   string
		values []string
		target map[string]struct{}
	}{
		{"approvedLabels", args.ApprovedLabels, keepSet},
		{"requiredLabels", args.RequiredLabels, requiredSet},
		{"metadataAttributes", args.MetadataAttributes, metadataSet},
		{"dropAttributes", args.DropAttributes, dropSet},
		{"lokiIndexLabels", indexLabels, indexSet},
	} {
		for _, name := range list.values {
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("%s must not contain blank resource attribute names", list.name)
			}
			list.target[name] = struct{}{}
		}
	}
	for name := range requiredSet {
		keepSet[name] = struct{}{}
	}
	for name := range metadataSet {
		if _, ok := keepSet[name]; ok {
			return nil, fmt.Errorf("attribute %q cannot be both retained as a resource and moved to metadata", name)
		}
	}
	for name := range dropSet {
		_, kept := keepSet[name]
		_, moved := metadataSet[name]
		if kept || moved {
			return nil, fmt.Errorf("attribute %q cannot be both dropped and retained or moved", name)
		}
	}
	sortedKeys := func(values map[string]struct{}) []string {
		keys := make([]string, 0, len(values))
		for name := range values {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		return keys
	}
	approved, required := sortedKeys(keepSet), sortedKeys(requiredSet)
	metadata, dropped := sortedKeys(metadataSet), sortedKeys(dropSet)

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
	type statementGroup struct {
		Context    string   `yaml:"context"`
		Statements []string `yaml:"statements"`
	}
	groups := []statementGroup{}
	if len(statements) > 0 {
		groups = append(groups, statementGroup{Context: "resource", Statements: statements})
	}
	if mode == "metadata" || len(metadata) > 0 {
		moves := []string{`set(cache["label_metadata"], resource.attributes)`}
		if mode == "metadata" {
			for _, name := range append(append([]string{}, approved...), dropped...) {
				moves = append(moves, "delete_key(cache[\"label_metadata\"], "+quoteAttribute(name)+")")
			}
		} else {
			selected := make([]string, 0, len(metadata))
			for _, name := range metadata {
				selected = append(selected, quoteAttribute(name))
			}
			moves = append(moves, "keep_keys(cache[\"label_metadata\"], ["+strings.Join(selected, ", ")+"])")
		}
		moves = append(moves, `merge_maps(attributes, cache["label_metadata"], "insert")`)
		groups = append(groups, statementGroup{Context: "log", Statements: moves})
	}
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
	cleanup := []string{}
	if mode == "drop" || mode == "metadata" {
		cleanup = append(cleanup, "keep_keys(attributes, ["+strings.Join(quoted, ", ")+"])")
	} else {
		for _, name := range append(append([]string{}, metadata...), dropped...) {
			cleanup = append(cleanup, "delete_key(attributes, "+quoteAttribute(name)+")")
		}
	}
	if len(cleanup) > 0 {
		groups = append(groups, statementGroup{Context: "resource", Statements: cleanup})
	}
	type processorConfig struct {
		ErrorMode     string           `yaml:"error_mode"`
		LogStatements []statementGroup `yaml:"log_statements"`
	}
	config := struct {
		Processors map[string]processorConfig `yaml:"processors"`
	}{
		Processors: map[string]processorConfig{
			processorName: {ErrorMode: "ignore", LogStatements: groups},
		},
	}
	rendered, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal Collector config: %w", err)
	}
	notes := []string{
		fmt.Sprintf("Requires the OpenTelemetry Collector contrib transform processor. Merge this processors block into your config and add %s to service.pipelines.logs.processors before the otlphttp exporter to Loki's /otlp endpoint. Loki must allow structured metadata (enabled by default in Loki 3.0+).", processorName),
		"This does not configure Loki index labels or generate Loki-side YAML. approvedLabels and requiredLabels express intent; Loki's existing OTLP mapping decides indexing. Loki may also discover a service_name label independently.",
		"Use native dotted OTel resource attribute names (for example service.name), not Loki-normalized names (service_name).",
		"Required resource attributes receive an unknown placeholder when missing or empty. Scope attributes and existing log attributes are not filtered; explicit drops affect resource attributes only.",
		"error_mode is ignore: failed statements are logged and skipped. Moves and removals are not transactional or fail-closed; values can remain unfiltered or be lost if copying fails before cleanup.",
		"Label guidance: https://grafana.com/docs/loki/latest/get-started/labels/ and https://grafana.com/docs/loki/latest/get-started/labels/bp-labels/ . Prefer bounded source attributes; normalizeLogLevel does not promote level to an index label.",
	}
	if args.LokiIndexLabels == nil {
		notes = append(notes, "Index guidance assumes Loki v3.5.0 defaults, including deployment.environment and deployment.environment.name. Source: https://github.com/grafana/loki/blob/v3.5.0/pkg/loghttp/push/otlp_config.go . Deployment and tenant overrides may differ; supply lokiIndexLabels to describe the effective resource index list.")
	} else {
		notes = append(notes, "Index guidance uses the supplied lokiIndexLabels as the complete effective resource index list, not additions to Loki defaults. It does not apply that list to Loki.")
	}
	switch mode {
	case "keep":
		notes = append(notes, "Unspecified resource attributes are kept unchanged; only dropAttributes and metadataAttributes remove resource originals.")
	case "metadata":
		notes = append(notes, "Other resource attributes are moved to log attributes before their resource originals are removed, excluding explicit drops.")
	case "drop":
		notes = append(notes, "Other resource attributes are deleted entirely, except explicit metadataAttributes which are moved first. They are not automatically preserved as structured metadata.")
	}
	if mode == "metadata" || len(metadata) > 0 {
		notes = append(notes, "Metadata moves preserve existing log values on key collisions, then remove resource originals after every record has been processed. Loki normally stores log attributes as structured metadata, but log-attribute drop rules can discard them. Moved values count toward per-line metadata limits.")
	}
	for _, name := range approved {
		if _, indexed := indexSet[name]; !indexed {
			notes = append(notes, fmt.Sprintf("Intended label %q is not in the effective Loki resource index list; retaining or requiring it does not promote it. It normally remains structured metadata.", name))
		}
	}
	potentialLabels := 0
	for _, name := range sortedKeys(indexSet) {
		_, kept := keepSet[name]
		_, moved := metadataSet[name]
		_, removed := dropSet[name]
		if moved || removed || (!kept && mode != "keep") {
			continue
		}
		potentialLabels++
		if !kept {
			notes = append(notes, fmt.Sprintf("Additional indexed attribute %q remains eligible when present, although it is not approved or required.", name))
		}
		if name == "k8s.pod.name" || name == "service.instance.id" {
			notes = append(notes, fmt.Sprintf("High-cardinality index candidate %q: Loki no longer recommends indexing it; consider metadataAttributes instead of deleting its value.", name))
		}
	}
	if potentialLabels > 15 {
		notes = append(notes, fmt.Sprintf("There are %d potential resource index labels, exceeding Loki's default limit of 15. They may not all be present together; actual labels and configured limits are unknown.", potentialLabels))
	}
	allNames := map[string]struct{}{}
	for _, names := range [][]string{approved, metadata, dropped, sortedKeys(indexSet)} {
		for _, name := range names {
			allNames[name] = struct{}{}
		}
	}
	for _, name := range sortedKeys(allNames) {
		if strings.Contains(name, "_") && !strings.Contains(name, ".") {
			notes = append(notes, fmt.Sprintf("Attribute %q may be Loki-normalized; verify its native OTel name. Custom names containing underscores are valid and are not rewritten.", name))
		}
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
	"Generates OpenTelemetry Collector contrib transform YAML preserving resource attributes by default, with explicit drops, moves to log metadata, optional filtering, required placeholders and log-level normalization. Reports Loki indexing assumptions without generating Loki-side YAML.",
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
