//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type otelTestProcessor struct {
	ErrorMode string `yaml:"error_mode"`
	Groups    []struct {
		Context    string   `yaml:"context"`
		Statements []string `yaml:"statements"`
	} `yaml:"log_statements"`
}

func decodeOTelProcessor(t *testing.T, text, name string) otelTestProcessor {
	t.Helper()
	var config struct {
		Processors map[string]otelTestProcessor `yaml:"processors"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(text))
	decoder.KnownFields(true)
	require.NoError(t, decoder.Decode(&config))
	require.Len(t, config.Processors, 1)
	require.Contains(t, config.Processors, name)
	return config.Processors[name]
}

func TestSuggestLokiOTelLabelConfigDefaults(t *testing.T) {
	result, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
		ApprovedLabels: []string{"service.name", "deployment.environment.name"},
	})
	require.NoError(t, err)
	processor := decodeOTelProcessor(t, result.YAML, "transform/enforce_labels")
	assert.Equal(t, "ignore", processor.ErrorMode)
	assert.Empty(t, processor.Groups)
	notes := strings.Join(result.Notes, "\n")
	for _, want := range []string{"service.pipelines.logs.processors", "contrib", "structured metadata", "does not configure Loki index labels", "kept unchanged", "v3.5.0", "service.name", "service_name", "normalizeLogLevel", "Additional indexed attribute", "default limit of 15", "error_mode is ignore"} {
		assert.Contains(t, notes, want)
	}
}

func TestSuggestLokiOTelLabelConfigUnionAndStability(t *testing.T) {
	firstArgs := SuggestLokiOTelLabelConfigParams{
		ApprovedLabels:  []string{"z", "service.name", "z"},
		RequiredLabels:  []string{"team", "service.name", "team", "a"},
		ProcessorName:   "transform/custom-logs.v2",
		OtherAttributes: "drop",
	}
	first, err := suggestLokiOTelLabelConfig(t.Context(), firstArgs)
	require.NoError(t, err)
	second, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
		ApprovedLabels:  []string{"service.name", "z"},
		RequiredLabels:  []string{"a", "team", "service.name"},
		ProcessorName:   firstArgs.ProcessorName,
		OtherAttributes: "drop",
	})
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, []string{"z", "service.name", "z"}, firstArgs.ApprovedLabels, "must not mutate caller input")
	processor := decodeOTelProcessor(t, first.YAML, firstArgs.ProcessorName)
	assert.Equal(t, []string{
		`set(attributes["a"], "unknown") where attributes["a"] == nil or attributes["a"] == ""`,
		`set(attributes["service.name"], "unknown") where attributes["service.name"] == nil or attributes["service.name"] == ""`,
		`set(attributes["team"], "unknown") where attributes["team"] == nil or attributes["team"] == ""`,
		`keep_keys(attributes, ["a", "service.name", "team", "z"])`,
	}, append(processor.Groups[0].Statements, processor.Groups[1].Statements...))
}

func TestSuggestLokiOTelLabelConfigNormalization(t *testing.T) {
	result, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
		ApprovedLabels: []string{"service.name"}, NormalizeLogLevel: true, ProcessorName: "transform",
	})
	require.NoError(t, err)
	processor := decodeOTelProcessor(t, result.YAML, "transform")
	require.Len(t, processor.Groups, 1)
	assert.Equal(t, "log", processor.Groups[0].Context)
	require.Len(t, processor.Groups[0].Statements, 4)

	stringLiteral := regexp.MustCompile(`"(?:\\.|[^"\\])*"`)
	patterns := map[string]*regexp.Regexp{}
	for _, statement := range processor.Groups[0].Statements {
		assert.True(t, strings.HasPrefix(statement, `replace_pattern(attributes["level"], `))
		assert.True(t, strings.HasSuffix(statement, ` where IsString(attributes["level"])`))
		literals := stringLiteral.FindAllString(statement, -1)
		require.Len(t, literals, 4)
		pattern, err := strconv.Unquote(literals[1])
		require.NoError(t, err)
		level, err := strconv.Unquote(literals[2])
		require.NoError(t, err)
		compiled, err := regexp.Compile(pattern)
		require.NoError(t, err)
		patterns[level] = compiled
	}
	for input, expected := range map[string]string{
		"I": "info", "Info": "info", "INFO": "info", "info": "info",
		"W": "warn", "Warn": "warn", "WARNING": "warn",
		"E": "error", "Err": "error", "ERROR": "error",
		"D": "debug", "DEBUG": "debug",
		"CRITICAL": "CRITICAL", "trace": "trace", "InfoExtra": "InfoExtra",
		"info\n": "info\n", "": "", "warning!": "warning!",
	} {
		got := input
		for level, pattern := range patterns {
			if pattern.MatchString(input) {
				got = level
			}
		}
		assert.Equal(t, expected, got, "input %q", input)
	}
	assert.Contains(t, strings.Join(result.Notes, "\n"), "level is not promoted")
	assert.NotContains(t, strings.Join(result.Notes, "\n"), "Consider enabling")
}

func TestSuggestLokiOTelLabelConfigInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args SuggestLokiOTelLabelConfigParams
		want string
	}{
		{"missing approved", SuggestLokiOTelLabelConfigParams{}, "at least one"},
		{"empty approved", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{}}, "at least one"},
		{"required only", SuggestLokiOTelLabelConfigParams{RequiredLabels: []string{"service.name"}}, "at least one"},
		{"empty attribute", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{""}}, "approvedLabels"},
		{"blank attribute", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name", " \n\t"}}, "approvedLabels"},
		{"blank required", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, RequiredLabels: []string{"\t"}}, "requiredLabels"},
		{"invalid mode", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, OtherAttributes: "remove"}, "otherAttributes"},
		{"blank metadata", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, MetadataAttributes: []string{" "}}, "metadataAttributes"},
		{"blank drop", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, DropAttributes: []string{""}}, "dropAttributes"},
		{"blank index", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, LokiIndexLabels: []string{""}}, "lokiIndexLabels"},
		{"approved metadata conflict", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, MetadataAttributes: []string{"service.name"}}, "both"},
		{"required metadata conflict", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, RequiredLabels: []string{"team"}, MetadataAttributes: []string{"team"}}, "both"},
		{"approved drop conflict", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, DropAttributes: []string{"service.name"}}, "both"},
		{"required drop conflict", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, RequiredLabels: []string{"team"}, DropAttributes: []string{"team"}}, "both"},
		{"metadata drop conflict", SuggestLokiOTelLabelConfigParams{ApprovedLabels: []string{"service.name"}, MetadataAttributes: []string{"pod"}, DropAttributes: []string{"pod"}}, "both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := suggestLokiOTelLabelConfig(t.Context(), tc.args)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, result)
		})
	}
	for _, name := range []string{" ", "custom", "attributes/enforce", "transform/", "transform/a/b", "transform/a\ninjected: true", "transform/a: b", "transform/${env:SECRET}", "transform/a#comment", "transform/.hidden", "transform/a\""} {
		t.Run(name, func(t *testing.T) {
			result, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
				ApprovedLabels: []string{"service.name"}, ProcessorName: name,
			})
			require.ErrorContains(t, err, "processorName")
			assert.Nil(t, result)
		})
	}
}

func TestSuggestLokiOTelLabelConfigPolicies(t *testing.T) {
	for _, mode := range []string{"keep", "metadata", "drop"} {
		t.Run(mode, func(t *testing.T) {
			result, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
				ApprovedLabels: []string{"service.name"}, RequiredLabels: []string{"team"},
				MetadataAttributes: []string{"k8s.pod.name", "k8s.pod.name"},
				DropAttributes:     []string{"secret", "secret"}, OtherAttributes: mode,
			})
			require.NoError(t, err)
			processor := decodeOTelProcessor(t, result.YAML, "transform/enforce_labels")
			assert.Equal(t, "ignore", processor.ErrorMode)
			require.Len(t, processor.Groups, 3)
			assert.Equal(t, "resource", processor.Groups[0].Context)
			assert.Equal(t, "log", processor.Groups[1].Context)
			assert.Equal(t, "resource", processor.Groups[2].Context)
			moves := processor.Groups[1].Statements
			assert.Equal(t, `set(cache["label_metadata"], resource.attributes)`, moves[0])
			assert.Equal(t, `merge_maps(attributes, cache["label_metadata"], "insert")`, moves[len(moves)-1])
			if mode == "metadata" {
				assert.Equal(t, []string{
					`set(cache["label_metadata"], resource.attributes)`,
					`delete_key(cache["label_metadata"], "service.name")`,
					`delete_key(cache["label_metadata"], "team")`,
					`delete_key(cache["label_metadata"], "secret")`,
					`merge_maps(attributes, cache["label_metadata"], "insert")`,
				}, moves)
			} else {
				assert.Contains(t, moves, `keep_keys(cache["label_metadata"], ["k8s.pod.name"])`)
			}
			if mode == "keep" {
				assert.Equal(t, []string{`delete_key(attributes, "k8s.pod.name")`, `delete_key(attributes, "secret")`}, processor.Groups[2].Statements)
			} else {
				assert.Equal(t, []string{`keep_keys(attributes, ["service.name", "team"])`}, processor.Groups[2].Statements)
			}
			assert.Contains(t, strings.Join(result.Notes, "\n"), "existing log values")
		})
	}
}

func TestSuggestLokiOTelLabelConfigIndexGuidance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		indexes []string
		want    string
		absent  string
	}{
		{"defaults", nil, `Additional indexed attribute "deployment.environment"`, "supplied lokiIndexLabels"},
		{"custom", []string{"service.name", "team", "custom.index", "custom.index"}, `Additional indexed attribute "custom.index"`, "assumes Loki v3.5.0 defaults"},
		{"empty", []string{}, `Intended label "service.name" is not in the effective`, "Additional indexed attribute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
				ApprovedLabels: []string{"service.name", "team"}, LokiIndexLabels: tc.indexes,
			})
			require.NoError(t, err)
			notes := strings.Join(result.Notes, "\n")
			assert.Contains(t, notes, tc.want)
			assert.NotContains(t, notes, tc.absent)
			if tc.name == "custom" {
				assert.NotContains(t, notes, `Intended label "team"`)
			}
		})
	}
	result, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
		ApprovedLabels: []string{"service_name"}, OtherAttributes: "metadata",
	})
	require.NoError(t, err)
	notes := strings.Join(result.Notes, "\n")
	assert.Contains(t, notes, `Attribute "service_name" may be Loki-normalized`)
	assert.NotContains(t, notes, "Additional indexed attribute")
	assert.NotContains(t, notes, "High-cardinality index candidate")
}

func TestSuggestLokiOTelLabelConfigEscaping(t *testing.T) {
	for _, name := range []string{
		`a"], "injected") where true //`,
		"quotes\"'\\\nnew:\n  processors: injected\r\t",
		"control\x00\a\v\x1f and unicode \u2028 ☃",
		"${env:SECRET}", "$${env:SECRET}", "${file:config.yaml}", "on", " a ",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
				ApprovedLabels: []string{name}, RequiredLabels: []string{name},
				OtherAttributes: "drop",
			})
			require.NoError(t, err)
			processor := decodeOTelProcessor(t, result.YAML, "transform/enforce_labels")
			require.Len(t, processor.Groups, 2)
			// Collector resolves $$ to a literal $ before parsing OTTL.
			statements := append(processor.Groups[0].Statements, processor.Groups[1].Statements...)
			for _, statement := range statements {
				assert.NotContains(t, statement, "\n", "attribute newlines must remain inside an escaped string")
			}
			literalPattern := regexp.MustCompile(`"(?:\\.|[^"\\])*"`)
			literals := literalPattern.FindAllString(statements[1], -1)
			require.Len(t, literals, 1, "the allowlist must contain exactly one OTTL string")
			decoded, err := strconv.Unquote(strings.ReplaceAll(literals[0], "$$", "$"))
			require.NoError(t, err)
			assert.Equal(t, name, decoded)
			literals = literalPattern.FindAllString(statements[0], -1)
			require.Len(t, literals, 5)
			for _, i := range []int{0, 2, 3} {
				decoded, err = strconv.Unquote(strings.ReplaceAll(literals[i], "$$", "$"))
				require.NoError(t, err)
				assert.Equal(t, name, decoded)
			}
		})
	}
}

func TestSuggestLokiOTelLabelConfigMCP(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	AddConfigTools(server)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	registered := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		registered[tool.Name] = tool
	}
	require.Contains(t, registered, "suggest_loki_alloy_label_config")
	require.Contains(t, registered, "suggest_loki_otel_label_config")
	tool := registered["suggest_loki_otel_label_config"]
	assert.True(t, tool.Annotations.ReadOnlyHint)
	assert.True(t, tool.Annotations.IdempotentHint)
	require.NotNil(t, tool.Annotations.DestructiveHint)
	assert.False(t, *tool.Annotations.DestructiveHint)
	require.NotNil(t, tool.Annotations.OpenWorldHint)
	assert.False(t, *tool.Annotations.OpenWorldHint)

	called, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: tool.Name, Arguments: map[string]any{"approvedLabels": []string{"service.name"}, "normalizeLogLevel": true},
	})
	require.NoError(t, err)
	require.False(t, called.IsError)
	require.NotEmpty(t, called.Content)
	text, ok := called.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var result SuggestLokiOTelLabelConfigResult
	require.NoError(t, json.Unmarshal([]byte(text.Text), &result))
	processor := decodeOTelProcessor(t, result.YAML, "transform/enforce_labels")
	assert.Len(t, processor.Groups, 1)

	called, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: tool.Name, Arguments: map[string]any{"approvedLabels": []string{}},
	})
	require.NoError(t, err)
	assert.True(t, called.IsError)
}
