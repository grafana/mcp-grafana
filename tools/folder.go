package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

type CreateFolderParams struct {
	Title     string `json:"title" jsonschema:"required,description=The title of the folder."`
	UID       string `json:"uid,omitempty" jsonschema:"description=Optional folder UID. If omitted\\, Grafana will generate one."`
	ParentUID string `json:"parentUid,omitempty" jsonschema:"description=Optional parent folder UID. If set\\, the folder will be created under this parent."`
}

func createFolder(ctx context.Context, args CreateFolderParams) (*models.Folder, error) {
	if args.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	c := mcpgrafana.GrafanaClientFromContext(ctx)
	cmd := &models.CreateFolderCommand{Title: args.Title}
	if args.UID != "" {
		cmd.UID = args.UID
	}
	if args.ParentUID != "" {
		cmd.ParentUID = args.ParentUID
	}

	resp, err := c.Folders.CreateFolder(cmd)
	if err != nil {
		return nil, fmt.Errorf("create folder '%s': %w", args.Title, err)
	}
	return resp.Payload, nil
}

var CreateFolder = mcpgrafana.MustTool(
	"create_folder",
	"Create a new folder in Grafana to organize dashboards and other resources. Use when the user wants to establish a new organizational structure or group related dashboards together. Accepts `title` (required string) and `uid` (optional unique identifier). e.g., title=\"Production Monitoring\" or uid=\"prod-folder-001\". Returns the created folder object with its assigned ID and metadata. Raises an error if the folder title already exists or the provided UID is already in use. Do not use when you need to list existing folders or modify folder permissions (use appropriate folder management tools instead).",
	createFolder,
	mcp.WithTitleAnnotation("Create folder"),
	mcp.WithIdempotentHintAnnotation(false),
)

func AddFolderTools(mcp *server.MCPServer, enableWriteTools bool) {
	if enableWriteTools {
		CreateFolder.Register(mcp)
	}
}
