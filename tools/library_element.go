package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/client/library_elements"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

// region GetLibraryElementByUID
type GetLibraryElementByUIDParams struct {
	UID string `json:"uid" jsonschema:"required,description=The UID of the library element"`
}

func getLibraryElementByUID(ctx context.Context, args GetLibraryElementByUIDParams) (*models.LibraryElementDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	libraryElement, err := c.LibraryElements.GetLibraryElementByUID(args.UID)
	if err != nil {
		return nil, fmt.Errorf("get library element by uid %s: %w", args.UID, err)
	}
	return libraryElement.Payload.Result, nil
}

var GetLibraryElementByUID = mcpgrafana.MustTool(
	"get_library_element_by_uid",
	"Retrieves the complete library element, including panels, variables, and settings, for a specific library element identified by its UID.",
	getLibraryElementByUID,
	mcp.WithTitleAnnotation("Returns a library element with the given UID"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
//endregion GetLibraryElementByUID

// region GetLibraryElementsByName
type GetLibraryElementsByNameParams struct {
	Name string `json:"name" jsonschema:"required,description=The name of the library element"`
}

func getLibraryElementsByName(ctx context.Context, args GetLibraryElementsByNameParams) ([]*models.LibraryElementDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)
	libraryElement, err := c.LibraryElements.GetLibraryElementByName(args.Name)
	if err != nil {
		return nil, fmt.Errorf("get library element by name %s: %w", args.Name, err)
	}
	return libraryElement.Payload.Result, nil
}

var GetLibraryElementsByName = mcpgrafana.MustTool(
	"get_library_elements_by_name",
	"Retrieves the complete library elements, including panels, variables, and settings, for a specific library elements identified by its name.",
	getLibraryElementsByName,
	mcp.WithTitleAnnotation("Returns a library elements with the given name"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
//endregion GetLibraryElementsByName

// region SearchLibraryElements
type SearchLibraryElementsParams struct {
	SearchString string `json:"searchString" jsonschema:"description=Part of the name or description searched for"`
	FolderFilterUIDs string `json:"folderFilterUIDs" jsonschema:"description=A comma separated list of folder UID(s) to filter the elements by"`
	ExcludeUid string `json:"excludeUid" jsonschema:"description=Element UID to exclude from search results"`
	SortDirection string `json:"sortDirection" jsonschema:"enum=alpha-asc,enum=alpha-desc,description=Sort order of elements"`
}

func searchLibraryElements(ctx context.Context, args SearchLibraryElementsParams) ([]*models.LibraryElementDTO, error) {
	c := mcpgrafana.GrafanaClientFromContext(ctx)

	params := library_elements.NewGetLibraryElementsParams().WithDefaults()
	if args.SearchString != "" {
		params.SetSearchString(&args.SearchString)
	}
	if args.FolderFilterUIDs != "" {
		params.SetFolderFilter(&args.FolderFilterUIDs)
	}
	if args.ExcludeUid != "" {
		params.SetExcludeUID(&args.ExcludeUid)
	}
    if args.SortDirection != "" {
        sortDirection := "alpha-asc"
        if args.SortDirection == "alpha-desc" {
            sortDirection = "alpha-desc"
        }
        params.SetSortDirection(&sortDirection)
    }

	result := []*models.LibraryElementDTO{}
	var totalCount int64
	for hasMorePages := true; hasMorePages; hasMorePages = ((*params.Page - 1) * *params.PerPage) < totalCount {
		search, err := c.LibraryElements.GetLibraryElements(params)
		if err != nil {
			return nil, fmt.Errorf("search library elements for %+v: %w", c, err)
		}
		result = append(result, search.Payload.Result.Elements...)
		totalCount = search.Payload.Result.TotalCount

		nextPage := *params.Page + 1
		params.SetPage(&nextPage)
	}

	return result, nil
}

var SearchLibraryElements = mcpgrafana.MustTool(
	"search_library_elements",
	"Returns a list of complete library elements, including panels, variables, and settings, for a specific library elements identified by query params.",
	searchLibraryElements,
	mcp.WithTitleAnnotation("Returns a library elements satisfies to query"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
//endregion SearchLibraryElements

func AddLibraryElementTools(mcp *server.MCPServer, enableWriteTools bool) {
	GetLibraryElementByUID.Register(mcp)
	GetLibraryElementsByName.Register(mcp)
	SearchLibraryElements.Register(mcp)
}
