package tools

import (
	"slices"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Policy says which tools a server registers. The zero value registers
// nothing, because it enables no category.
type Policy struct {
	// Categories are the enabled tool categories. Unknown names are ignored.
	Categories []string
	// ReadOnly removes the tools that write, except those named in Allow.
	ReadOnly bool
	// NoQuery removes the tools that run a datasource query. Allow does not
	// bring them back.
	NoQuery bool
	// Allow names the tools ReadOnly keeps. For a tool with read-only
	// variants, naming it keeps the variant that writes.
	Allow []string
}

// ActiveCategory is an enabled category that registers at least one tool.
type ActiveCategory struct {
	Name string
	// Description is the line the server instructions use for the
	// category, shortened when the policy removed some of its tools.
	Description string
}

// Selection is the result of resolving a Policy against the catalog.
type Selection struct {
	Tools      []mcpgrafana.Tool
	Categories []ActiveCategory
}

type resolvedCategory struct {
	*category
	reduced       bool
	listed        bool
	hostsViewer   bool
	appSourceOnly bool
}

// Resolve picks, for every tool in an enabled category, the most capable
// variant the policy allows.
func Resolve(p Policy) Selection {
	var sel Selection
	var sources []mcpgrafana.Tool
	var resolved []resolvedCategory
	for i := range catalog {
		if !slices.Contains(p.Categories, catalog[i].name) {
			continue
		}
		r := resolvedCategory{category: &catalog[i]}
		for _, spec := range r.tools {
			if spec.kind == appViewer {
				r.hostsViewer = true
				continue
			}
			picked := slices.IndexFunc(spec.variants, p.allows)
			r.reduced = r.reduced || picked != 0
			if picked < 0 {
				continue
			}
			tool := spec.variants[picked].tool
			if spec.kind == appSource || spec.kind == appSourceOnly {
				sources = append(sources, tool)
			}
			if spec.kind == appSourceOnly {
				r.appSourceOnly = true
				continue
			}
			sel.Tools = append(sel.Tools, tool)
			r.listed = true
		}
		resolved = append(resolved, r)
	}

	viewerBuilt := len(sources) > 0 && slices.ContainsFunc(resolved, func(r resolvedCategory) bool { return r.hostsViewer })
	if viewerBuilt {
		sel.Tools = append(sel.Tools, newAppTool(sources...))
	}
	for _, r := range resolved {
		exposed := r.listed || viewerBuilt && (r.hostsViewer || r.appSourceOnly)
		if !exposed {
			continue
		}
		description := r.description
		if r.reduced && r.reducedDescription != "" {
			description = r.reducedDescription
		}
		sel.Categories = append(sel.Categories, ActiveCategory{Name: r.name, Description: description})
	}
	return sel
}

func (p Policy) allows(v variant) bool {
	if v.gate&gateQuery != 0 && p.NoQuery {
		return false
	}
	if v.gate&gateWrite == 0 || !p.ReadOnly {
		return true
	}
	return slices.ContainsFunc(v.liftedBy, func(name string) bool { return slices.Contains(p.Allow, name) })
}

// Register adds the selected tools to srv.
func (s Selection) Register(srv *mcp.Server) {
	for _, t := range s.Tools {
		t.Register(srv)
	}
}
