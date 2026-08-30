package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestMainBindsExactlyBoundServices is the other half of the §65.10
// auto-inventory gate (grok war-room P2). The inventory reflection test
// reflects over app.BoundServices(); this test proves main.go's actual
// application.Service list binds exactly those same constructors. A
// service added to main.go but not to app.BoundServices() (so
// un-inventoried and un-gated), or vice versa, fails here.
func TestMainBindsExactlyBoundServices(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Map every short-var assignment `foo := app.NewXxx(...)` so a service
	// bound via a variable (transferService, settingsService, …) resolves
	// to its constructor.
	varCtor := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || i >= len(as.Rhs) {
				continue
			}
			if ctor := appConstructor(as.Rhs[i]); ctor != "" {
				varCtor[id.Name] = ctor
			}
		}
		return true
	})

	// Find application.NewService(arg) calls and resolve each arg to the
	// constructor of the service it binds.
	bound := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "application", "NewService") || len(call.Args) != 1 {
			return true
		}
		switch arg := call.Args[0].(type) {
		case *ast.CallExpr: // application.NewService(app.NewXxx(...))
			if ctor := appConstructor(arg); ctor != "" {
				bound[ctor] = true
			}
		case *ast.Ident: // application.NewService(transferService)
			if ctor := varCtor[arg.Name]; ctor != "" {
				bound[ctor] = true
			} else {
				t.Errorf("main.go binds variable %q whose constructor could not be resolved", arg.Name)
			}
		default:
			t.Errorf("main.go binds an unexpected expression %T", arg)
		}
		return true
	})

	want := map[string]bool{}
	for _, b := range app.BoundServices() {
		want[b.Constructor] = true
	}
	if missing := diff(want, bound); len(missing) > 0 {
		t.Errorf("app.BoundServices lists constructors main.go does not bind: %v", missing)
	}
	if extra := diff(bound, want); len(extra) > 0 {
		t.Errorf("main.go binds services absent from app.BoundServices (un-inventoried): %v", extra)
	}
}

// appConstructor returns "NewXxx" if e is a call to app.NewXxx(...), else "".
func appConstructor(e ast.Expr) string {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "app" {
		return ""
	}
	return sel.Sel.Name
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}

func diff(a, b map[string]bool) []string {
	var only []string
	for k := range a {
		if !b[k] {
			only = append(only, k)
		}
	}
	sort.Strings(only)
	return only
}
