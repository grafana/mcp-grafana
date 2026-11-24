package tools

import (
	"errors"
	"fmt"
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
	// Deny names tools to remove, with all their variants.
	Deny []string
}

// Validate reports every name in p that would have no effect: an unknown
// category, an unknown tool in Allow or Deny, a tool in Allow that no
// enabled category can register, and a tool in both Allow and Deny. A tool
// in Allow must be in an enabled category itself, even when it also lifts a
// variant of a tool in another category.
// Resolve itself ignores such names, so only callers that want strict
// input call this.
func (p Policy) Validate() error {
	home := map[string]string{}
	for _, c := range catalog {
		for _, spec := range c.tools {
			if spec.kind == appViewer {
				home[appToolName] = c.name
			}
			for _, v := range spec.variants {
				home[v.tool.Tool.Name] = c.name
			}
		}
	}

	var errs []error
	for _, name := range p.Categories {
		if !slices.Contains(CategoryNames(), name) {
			errs = append(errs, fmt.Errorf("unknown category %q", name))
		}
	}
	for _, name := range p.Allow {
		switch {
		case home[name] == "":
			errs = append(errs, fmt.Errorf("allow: unknown tool %q", name))
		case !slices.Contains(p.Categories, home[name]):
			errs = append(errs, fmt.Errorf("allow: tool %q is in a category that is not enabled (%s)", name, home[name]))
		case slices.Contains(p.Deny, name):
			errs = append(errs, fmt.Errorf("tool %q is in both allow and deny", name))
		}
	}
	for _, name := range p.Deny {
		if home[name] == "" {
			errs = append(errs, fmt.Errorf("deny: unknown tool %q", name))
		}
	}
	return errors.Join(errs...)
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
	// vizRenderer is set when the App tool has query_datasource as a source, so Register adds the tool that
	// declares the viz Renderer. It is not in Tools: it is for the App, not for the model.
	vizRenderer bool
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
		// A denied tool shortens the description only once no tool behind
		// a gate is left, as when the gate itself removes them.
		deniedGated, keptGated := false, false
		for _, spec := range r.tools {
			if spec.kind == appViewer {
				r.hostsViewer = true
				continue
			}
			gated := spec.variants[0].gate != 0
			if slices.Contains(p.Deny, spec.variants[0].tool.Tool.Name) {
				deniedGated = deniedGated || gated
				continue
			}
			picked := slices.IndexFunc(spec.variants, p.allows)
			r.reduced = r.reduced || picked != 0
			keptGated = keptGated || gated && picked == 0
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
		r.reduced = r.reduced || deniedGated && !keptGated
		resolved = append(resolved, r)
	}

	viewerBuilt := !slices.Contains(p.Deny, appToolName) && len(sources) > 0 && slices.ContainsFunc(resolved, func(r resolvedCategory) bool { return r.hostsViewer })
	if viewerBuilt {
		sel.Tools = append(sel.Tools, newAppTool(sources...))
		sel.vizRenderer = readsVizRenderer(sources)
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
	if s.vizRenderer {
		mcpgrafana.RegisterVizRendererTool(srv)
	}
}
