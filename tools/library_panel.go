package tools

import (
	"context"
	"fmt"
	"maps"

	"github.com/grafana/grafana-openapi-client-go/client/library_elements"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// libraryPanelUID returns the uid of the library element a panel points at, or
// "" for an ordinary panel. A dashboard stores only this reference; the panel's
// targets and datasource live in the library element.
func libraryPanelUID(panel map[string]interface{}) string {
	return safeString(safeObject(panel, "libraryPanel"), "uid")
}

// libraryPanelCache holds the library panel models fetched while serving one
// tool call, so a library panel used several times on a dashboard is fetched
// once. Failures are cached too, so a missing element is not retried per panel.
type libraryPanelCache struct {
	models map[string]libraryPanelEntry
}

type libraryPanelEntry struct {
	model map[string]interface{}
	err   error
}

func newLibraryPanelCache() *libraryPanelCache {
	return &libraryPanelCache{models: make(map[string]libraryPanelEntry)}
}

func (c *libraryPanelCache) fetch(ctx context.Context, uid string) (map[string]interface{}, error) {
	if entry, ok := c.models[uid]; ok {
		return entry.model, entry.err
	}
	model, err := fetchLibraryPanelModel(ctx, uid)
	c.models[uid] = libraryPanelEntry{model: model, err: err}
	return model, err
}

// fetchLibraryPanelModel returns the model of a library element, which has the
// shape of a classic (v1) panel: targets, datasource, fieldConfig and so on.
func fetchLibraryPanelModel(ctx context.Context, uid string) (map[string]interface{}, error) {
	client := mcpgrafana.GrafanaClientFromContext(ctx)
	if client == nil {
		return nil, fmt.Errorf("no Grafana client available")
	}
	res, err := client.LibraryElements.GetLibraryElementByUIDWithParams(
		library_elements.NewGetLibraryElementByUIDParamsWithContext(ctx).WithLibraryElementUID(uid),
	)
	if err != nil {
		return nil, err
	}
	if res.Payload == nil || res.Payload.Result == nil {
		return nil, fmt.Errorf("empty response")
	}
	model, ok := res.Payload.Result.Model.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("library element has no panel model")
	}
	return model, nil
}

// resolveLibraryPanel returns the panel to read queries from. An ordinary panel
// is returned as is. A library panel is replaced by a copy of its library
// element's model that keeps the dashboard's panel id and, when set, title.
// Failure to fetch the element is an error that names the library panel uid, so
// the panel is never dropped silently.
func resolveLibraryPanel(ctx context.Context, cache *libraryPanelCache, panel map[string]interface{}) (map[string]interface{}, error) {
	uid := libraryPanelUID(panel)
	if uid == "" {
		return panel, nil
	}
	model, err := cache.fetch(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("resolving library panel %q: %w", uid, err)
	}
	resolved := maps.Clone(model)
	if id, ok := panel["id"]; ok {
		resolved["id"] = id
	}
	if title := safeString(panel, "title"); title != "" {
		resolved["title"] = title
	}
	return resolved, nil
}

// libraryPanelFailure reports an unresolvable library panel in a listing of
// queries, as an entry with a warning instead of dropping the panel.
func libraryPanelFailure(panel map[string]interface{}, err error) panelQuery {
	return panelQuery{
		Title:    safeString(panel, "title"),
		Warnings: []string{err.Error()},
	}
}
