package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	openapi "github.com/grafana/mcp-grafana/internal/linter/openapi"
)

func main() {
	singlechecker.Main(openapi.Analyzer)
}
