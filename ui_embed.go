package mcpgrafana

import _ "embed"

//go:embed ui/panel-viewer/dist/mcp-app.html
var panelViewerAppHTML string

//go:embed ui/insight-cell/dist/mcp-app.html
var insightCellAppHTML string
