package tools

import (
	"context"
	"fmt"

	"github.com/grafana/grafana-openapi-client-go/client/folders"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

	resp, err := c.Folders.CreateFolderWithParams(
		folders.NewCreateFolderParamsWithContext(ctx).WithBody(cmd),
	)
	if err != nil {
		return nil, fmt.Errorf("create folder '%s': %w", args.Title, err)
	}
	return resp.Payload, nil
}

var CreateFolder = mcpgrafana.MustTool(
	"create_folder",
	"Create a Grafana folder. Provide a title and optional UID. Returns the created folder.",
	createFolder,
	mcpgrafana.WithTitleAnnotation("Create folder"),
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
)

func AddFolderTools(mcp *mcp.Server, enableWriteTools bool) {
	if enableWriteTools {
		CreateFolder.Register(mcp)
	}
}
