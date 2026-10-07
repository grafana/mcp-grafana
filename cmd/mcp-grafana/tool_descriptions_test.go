package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// directivePatterns match wording that instructs the model how to behave
// rather than describing what a tool does. Connector directories (e.g.
// Anthropic's) reject tool descriptions containing such instructions, so this
// guidance belongs in tool results or server instructions instead.
var directivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bMUST\b`),
	regexp.MustCompile(`(?i)\b(IMPORTANT|CRITICAL):`),
	regexp.MustCompile(`(?i)\b(ask|tell|remind|inform|confirm(ed)? with) the user\b`),
	regexp.MustCompile(`(?i)\bcall (this|it)( tool)? first\b`),
	regexp.MustCompile(`(?i)\b(do not|don't|never) (call|use|infer)\b`),
	regexp.MustCompile(`(?i)\byou (should|must)\b`),
	regexp.MustCompile(`(?i)\bbefore considering\b`),
}

// TestToolDescriptionsAreSelfContained checks that no tool or parameter
// description names another tool or tells the model how to behave.
func TestToolDescriptionsAreSelfContained(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	for _, entry := range (&disabledTools{}).toolEntries() {
		entry.adder(s)
	}
	result, err := connectTestClient(t, s).ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, result.Tools)

	names := make([]*regexp.Regexp, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, regexp.MustCompile(`\b`+regexp.QuoteMeta(tool.Name)+`\b`))
	}

	for _, tool := range result.Tools {
		descs := map[string]string{"description": tool.Description}
		b, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema any
		require.NoError(t, json.Unmarshal(b, &schema))
		collectDescriptions(schema, "inputSchema", descs)

		for path, desc := range descs {
			for _, re := range names {
				if re.String() != `\b`+regexp.QuoteMeta(tool.Name)+`\b` && re.MatchString(desc) {
					t.Errorf("%s %s names another tool (%s): %q", tool.Name, path, re.FindString(desc), desc)
				}
			}
			for _, re := range directivePatterns {
				if re.MatchString(desc) {
					t.Errorf("%s %s contains a model directive (%s): %q", tool.Name, path, re.FindString(desc), desc)
				}
			}
		}
	}
}

func collectDescriptions(v any, path string, out map[string]string) {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			if s, ok := child.(string); ok && k == "description" {
				out[path+".description"] = s
				continue
			}
			collectDescriptions(child, path+"."+k, out)
		}
	case []any:
		for i, child := range v {
			collectDescriptions(child, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}
