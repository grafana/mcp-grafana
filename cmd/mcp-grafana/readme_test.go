package main

import (
	"flag"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

var updateReadme = flag.Bool("update-readme", false, "rewrite the README tool table's permissions column")

const readmePath = "../../README.md"

// TestReadmeToolPermissions keeps the README tool table's "Required RBAC
// Permissions" column in step with each tool's RequiresPermissions. Run
// `go test ./cmd/mcp-grafana -run TestReadmeToolPermissions -update-readme`
// to regenerate it. The other columns are written by hand.
func TestReadmeToolPermissions(t *testing.T) {
	declared := make(map[string][]string)
	for name, tool := range listAllCategoryTools(t, disabledTools{}) {
		declared[name], _ = mcpgrafana.ToolRequiredPermissions(tool)
	}

	raw, err := os.ReadFile(readmePath)
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")

	start := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "| Tool ") })
	require.NotEqual(t, -1, start, "README tool table not found")
	end := start
	for end < len(lines) && strings.HasPrefix(lines[end], "|") {
		end++
	}

	var rows [][]string
	listed := make(map[string]bool)
	for i, line := range lines[start:end] {
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		if i > 1 {
			name := strings.Trim(cells[0], "`")
			perms, ok := declared[name]
			if !assert.True(t, ok, "README lists %q, which is not a registered tool", name) {
				continue
			}
			listed[name] = true
			cells[3] = formatPermissions(perms)
		}
		rows = append(rows, cells)
	}
	for name := range declared {
		assert.True(t, listed[name], "tool %q has no row in the README tool table", name)
	}

	want := strings.Join(slices.Concat(lines[:start], formatTable(rows), lines[end:]), "\n")
	if *updateReadme {
		require.NoError(t, os.WriteFile(readmePath, []byte(want), 0o644))
		return
	}
	assert.Equal(t, string(raw), want, "README tool permissions are out of date; regenerate with "+
		"`go test ./cmd/mcp-grafana -run TestReadmeToolPermissions -update-readme`")
}

func formatPermissions(perms []string) string {
	if len(perms) == 0 {
		return "None"
	}
	quoted := make([]string, len(perms))
	for i, p := range perms {
		quoted[i] = "`" + p + "`"
	}
	return strings.Join(quoted, ", ")
}

// formatTable pads a markdown table so its columns line up. Row 1 is the
// separator row.
func formatTable(rows [][]string) []string {
	widths := make([]int, len(rows[0]))
	for i, row := range rows {
		for j, cell := range row {
			if i != 1 {
				widths[j] = max(widths[j], len([]rune(cell)))
			}
		}
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		var b strings.Builder
		b.WriteString("|")
		for j, cell := range row {
			if i == 1 {
				cell = strings.Repeat("-", widths[j])
			}
			b.WriteString(" " + cell + strings.Repeat(" ", widths[j]-len([]rune(cell))) + " |")
		}
		out[i] = b.String()
	}
	return out
}
