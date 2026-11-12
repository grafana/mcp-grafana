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
	declared := make(map[string]string)
	readOnly := listAllCategoryTools(t, disabledTools{write: true})
	for name, tool := range listAllCategoryTools(t, disabledTools{}) {
		perms, _ := mcpgrafana.ToolRequiredPermissions(tool)
		var roPerms []string
		if ro, ok := readOnly[name]; ok {
			roPerms, _ = mcpgrafana.ToolRequiredPermissions(ro)
		}
		declared[name] = formatPermissions(perms, roPerms)
	}
	for name, tool := range readOnly {
		if _, ok := declared[name]; !ok {
			perms, _ := mcpgrafana.ToolRequiredPermissions(tool)
			declared[name] = formatPermissions(perms, perms)
		}
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
		cells := splitTableRow(line)
		if i > 0 && !assert.Len(t, cells, len(rows[0]), "README tool table row has the wrong number of cells: %s", line) {
			continue
		}
		if i > 1 {
			name := strings.Trim(cells[0], "`")
			perms, ok := declared[name]
			if !assert.True(t, ok, "README lists %q, which is not a registered tool", name) {
				continue
			}
			listed[name] = true
			cells[3] = perms
		}
		rows = append(rows, cells)
	}
	for name := range declared {
		assert.True(t, listed[name], "tool %q has no row in the README tool table", name)
	}

	want := strings.Join(slices.Concat(lines[:start], formatTable(rows), lines[end:]), "\n")
	if t.Failed() {
		return
	}
	if *updateReadme {
		require.NoError(t, os.WriteFile(readmePath, []byte(want), 0o644))
		return
	}
	assert.Equal(t, string(raw), want, "README tool permissions are out of date; regenerate with "+
		"`go test ./cmd/mcp-grafana -run TestReadmeToolPermissions -update-readme`")
}

// TestDocsToolTableListsRegisteredTools checks that the docs reference table
// lists exactly the registered tools. Its columns are written by hand.
func TestDocsToolTableListsRegisteredTools(t *testing.T) {
	var registered []string
	for name := range listAllCategoryTools(t, disabledTools{}) {
		registered = append(registered, name)
	}

	raw, err := os.ReadFile("../../docs/sources/reference/mcp-tools-table.md")
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")
	start := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "| Tool ") })
	require.NotEqual(t, -1, start, "docs tool table not found")
	var documented []string
	for _, line := range lines[start+2:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		documented = append(documented, strings.Trim(splitTableRow(line)[0], "`"))
	}

	assert.ElementsMatch(t, registered, documented, "docs/sources/reference/mcp-tools-table.md is out of sync with the registered tools")
}

// splitTableRow splits a markdown table row into trimmed cells, leaving
// escaped pipes (\|) inside their cell.
func splitTableRow(line string) []string {
	var cells []string
	var cell strings.Builder
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cell.WriteString(`\|`)
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// formatPermissions renders a tool's actions. When the read-only variant
// (--disable-write) needs fewer, the extra write actions are listed apart so
// read-only deployments don't over-grant.
func formatPermissions(readWrite, readOnly []string) string {
	writeOnly := slices.DeleteFunc(slices.Clone(readWrite), func(p string) bool { return slices.Contains(readOnly, p) })
	if readOnly == nil || len(writeOnly) == 0 || len(readOnly)+len(writeOnly) != len(readWrite) {
		return quotePermissions(readWrite)
	}
	return quotePermissions(readOnly) + "; with writes enabled also " + quotePermissions(writeOnly)
}

func quotePermissions(perms []string) string {
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

func TestReadmeTableHelpers(t *testing.T) {
	assert.Equal(t, []string{"`a`", `x \| y`, ""}, splitTableRow("| `a` | x \\| y |  |"))
	assert.Equal(t, "None", formatPermissions(nil, nil))
	assert.Equal(t, "`a`, `b`", formatPermissions([]string{"a", "b"}, []string{"a", "b"}))
	assert.Equal(t, "`a`; with writes enabled also `b`", formatPermissions([]string{"a", "b"}, []string{"a"}))
}
