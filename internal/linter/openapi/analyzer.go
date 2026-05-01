package openapi

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const clientPkgPrefix = "github.com/grafana/grafana-openapi-client-go/client/"

var Analyzer = &analysis.Analyzer{
	Name:     "openapicontext",
	Doc:      "checks that openapi client calls use the WithParams variant to propagate request context",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{(*ast.CallExpr)(nil)}

	insp.Preorder(nodeFilter, func(n ast.Node) {
		call := n.(*ast.CallExpr)

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}

		methodName := sel.Sel.Name
		if strings.HasSuffix(methodName, "WithParams") {
			return
		}

		// Skip test files
		pos := pass.Fset.Position(call.Pos())
		if strings.HasSuffix(pos.Filename, "_test.go") {
			return
		}

		selection, ok := pass.TypesInfo.Selections[sel]
		if !ok {
			return
		}
		if selection.Kind() != types.MethodVal {
			return
		}

		recvType := selection.Recv()
		if !isOpenAPIClientType(recvType) {
			return
		}

		withParamsName := methodName + "WithParams"
		mset := types.NewMethodSet(recvType)
		for i := range mset.Len() {
			if mset.At(i).Obj().Name() == withParamsName {
				pass.Reportf(call.Pos(),
					"use %s with a context-aware params object instead of %s which drops request context",
					withParamsName, methodName)
				return
			}
		}
	})

	return nil, nil
}

func isOpenAPIClientType(t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	pkg := named.Obj().Pkg()
	if pkg == nil {
		return false
	}
	return strings.HasPrefix(pkg.Path(), clientPkgPrefix)
}
