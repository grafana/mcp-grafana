// Package datasource looks up Grafana datasources for the caller in ctx,
// falling back to /api/frontend/settings when the datasource metadata API is
// inaccessible to the token (see fallback.go).
package datasource

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/grafana/grafana-openapi-client-go/client/datasources"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// ErrNotFound reports that the requested datasource does not exist or is not
// visible to the caller. Match it with errors.Is.
var ErrNotFound = errors.New("datasource not found")

// notFoundError keeps the message tools have always returned while matching
// ErrNotFound.
type notFoundError struct{ field, value string }

func (e notFoundError) Error() string {
	return fmt.Sprintf("datasource with %s '%s' not found. Please check if the datasource exists and is accessible", e.field, e.value)
}

func (notFoundError) Is(target error) bool { return target == ErrNotFound }

// List returns every datasource visible to the caller.
func List(ctx context.Context) (models.DataSourceList, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	resp, err := c.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(ctx),
	)
	if err == nil {
		return resp.Payload, nil
	}
	if fb, fbErr := fallbackDatasourceList(ctx); fbErr == nil {
		// The datasources API is not accessible to this token (e.g. it
		// requires Org Admin before Grafana 9.0); fall back to frontend settings
		// (see fallback.go).
		return fb, nil
	}
	return nil, fmt.Errorf("list datasources: %w", err)
}

// Default returns the organization's default datasource.
func Default(ctx context.Context) (*models.DataSourceListItemDTO, error) {
	list, err := List(ctx)
	if err != nil {
		return nil, err
	}
	for _, ds := range list {
		if ds.IsDefault {
			return ds, nil
		}
	}
	return nil, errors.New("no default datasource is configured")
}

// ByUID returns the datasource with the given uid, or an error matching
// ErrNotFound.
func ByUID(ctx context.Context, uid string) (*models.DataSource, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByUIDWithParams(
		datasources.NewGetDataSourceByUIDParamsWithContext(ctx).WithUID(uid),
	)
	if err != nil {
		var status interface{ IsCode(int) bool }
		if errors.As(err, &status) && status.IsCode(http.StatusNotFound) {
			return nil, notFoundError{"UID", uid}
		}
		// The datasource metadata API is not accessible to this token (e.g.
		// it requires Org Admin before Grafana 9.0); fall back to frontend
		// settings (see fallback.go).
		ds, fbErr := fallbackDatasourceByUID(ctx, uid)
		if fbErr == nil {
			return ds, nil
		}
		if errors.Is(fbErr, errFallbackDatasourceNotFound) {
			// The settings were readable and the datasource is genuinely
			// absent: report not-found rather than the misleading permission
			// error, so agents can tell a typo from a credentials problem.
			return nil, notFoundError{"UID", uid}
		}
		return nil, fmt.Errorf("get datasource by uid %s: %w", uid, err)
	}
	return datasource.Payload, nil
}

// ByName returns the datasource with the given name, or an error matching
// ErrNotFound.
func ByName(ctx context.Context, name string) (*models.DataSource, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	datasource, err := c.Datasources.GetDataSourceByNameWithParams(
		datasources.NewGetDataSourceByNameParamsWithContext(ctx).WithName(name),
	)
	if err != nil {
		// The datasource metadata API is not accessible to this token (e.g.
		// it requires Org Admin before Grafana 9.0); fall back to frontend
		// settings (see fallback.go).
		ds, fbErr := fallbackDatasourceByName(ctx, name)
		if fbErr == nil {
			return ds, nil
		}
		if errors.Is(fbErr, errFallbackDatasourceNotFound) {
			return nil, notFoundError{"name", name}
		}
		return nil, fmt.Errorf("get datasource by name %s: %w", name, err)
	}
	return datasource.Payload, nil
}
