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
	assert.Equal(t, "propagate", processor.ErrorMode)
	require.Len(t, processor.Groups, 1)
	assert.Equal(t, "resource", processor.Groups[0].Context)
	assert.Equal(t, []string{`keep_keys(attributes, ["deployment.environment.name", "service.name"])`}, processor.Groups[0].Statements)
	notes := strings.Join(result.Notes, "\n")
	for _, want := range []string{"service.pipelines.logs.processors", "contrib", "structured metadata", "does not configure Loki index labels", "limits_config.otlp_config.resource_attributes", "action: index_label", "ignore_defaults", "service.name", "service_name", "normalizeLogLevel"} {
		assert.Contains(t, notes, want)
	}
}

func TestSuggestLokiOTelLabelConfigUnionAndStability(t *testing.T) {
	firstArgs := SuggestLokiOTelLabelConfigParams{
		ApprovedLabels: []string{"z", "service.name", "z"},
		RequiredLabels: []string{"team", "service.name", "team", "a"},
		ProcessorName:  "transform/custom-logs.v2",
	}
	first, err := suggestLokiOTelLabelConfig(t.Context(), firstArgs)
	require.NoError(t, err)
	second, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
		ApprovedLabels: []string{"service.name", "z"},
		RequiredLabels: []string{"a", "team", "service.name"},
		ProcessorName:  firstArgs.ProcessorName,
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
	}, processor.Groups[0].Statements)
}

func TestSuggestLokiOTelLabelConfigNormalization(t *testing.T) {
	result, err := suggestLokiOTelLabelConfig(t.Context(), SuggestLokiOTelLabelConfigParams{
		ApprovedLabels: []string{"service.name"}, NormalizeLogLevel: true, ProcessorName: "transform",
	})
	require.NoError(t, err)
	processor := decodeOTelProcessor(t, result.YAML, "transform")
	require.Len(t, processor.Groups, 2)
	assert.Equal(t, "resource", processor.Groups[0].Context)
	assert.Equal(t, "log", processor.Groups[1].Context)
	require.Len(t, processor.Groups[1].Statements, 4)

	stringLiteral := regexp.MustCompile(`"(?:\\.|[^"\\])*"`)
	patterns := map[string]*regexp.Regexp{}
	for _, statement := range processor.Groups[1].Statements {
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
			})
			require.NoError(t, err)
			processor := decodeOTelProcessor(t, result.YAML, "transform/enforce_labels")
			require.Len(t, processor.Groups, 1)
			require.Len(t, processor.Groups[0].Statements, 2)
			// Collector resolves $$ to a literal $ before parsing OTTL.
			statements := processor.Groups[0].Statements
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
	assert.Len(t, processor.Groups, 2)

	called, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: tool.Name, Arguments: map[string]any{"approvedLabels": []string{}},
	})
	require.NoError(t, err)
	assert.True(t, called.IsError)
}
