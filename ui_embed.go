package mcpgrafana

import _ "embed"

//go:embed ui/panel-viewer/dist/mcp-app.html
var panelViewerAppHTML string

//go:embed ui/panel-embed/dist/mcp-app.html
var panelEmbedAppHTML string
