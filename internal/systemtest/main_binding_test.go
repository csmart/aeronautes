package systemtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// TestMainBindsExactlyBoundServices is the other half of the §65.10
// auto-inventory gate (grok war-room P2). TestInventoryCoversEveryBoundAction
// reflects over app.BoundServices(); this test proves main.go's actual
// application.Service list binds exactly those same constructors. A
// service added to main.go but not to app.BoundServices() (so
// un-inventoried and un-gated), or vice versa, fails here.
//
// It reads main.go as SOURCE rather than living in package main, whose
// //go:embed frontend/dist would force a frontend build in the SAIO test
// job just to compile the test binary. The path is resolved from this
// file's own location so it holds regardless of the test's working dir.
func TestMainBindsExactlyBoundServices(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file to find main.go")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	mainPath := filepath.Join(repoRoot, "main.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainPath, nil, 0)
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
		if !ok || !isSelectorCall(call.Fun, "application", "NewService") || len(call.Args) != 1 {
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
	if missing := onlyIn(want, bound); len(missing) > 0 {
		t.Errorf("app.BoundServices lists constructors main.go does not bind: %v", missing)
	}
	if extra := onlyIn(bound, want); len(extra) > 0 {
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

func isSelectorCall(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}

func onlyIn(a, b map[string]bool) []string {
	var only []string
	for k := range a {
		if !b[k] {
			only = append(only, k)
		}
	}
	sort.Strings(only)
	return only
}
