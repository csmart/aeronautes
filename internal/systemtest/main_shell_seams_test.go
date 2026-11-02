package systemtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestMainWiresEverySeamTheShellMustSupply is the gate a real defect walked
// through. internal/app exposed OverwriteConfirmer, wiring.go exposed
// WireOverwriteConfirmer, the admission refused without one — and main.go
// never called it, so the shipped build could upload a new name and could not
// replace one, while every test in the tree wired its own fake and passed.
// A seam that exists and is never wired is invisible to a test binary by
// construction, which is exactly why it needs a source-level check.
//
// The set is DERIVED, not listed. An exported interface in internal/app is
// something an implementation outside that package must satisfy, and in the
// product the only implementor is main.go; a wiring function whose parameter
// is one of those interfaces is how it gets there. The fixture hooks in
// wiring.go take funcs rather than interfaces and are deliberately not wired
// in the product, so they fall out of the derivation rather than needing an
// exception. Adding a shell seam therefore adds a requirement here by itself.
//
// Like TestMainBindsExactlyBoundServices this reads main.go as SOURCE: being
// in package main would force a frontend build just to compile the test
// binary. Paths come from this file's own location.
func TestMainWiresEverySeamTheShellMustSupply(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file to find the sources")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	fset := token.NewFileSet()
	appPkg, err := parser.ParseDir(fset, filepath.Join(repoRoot, "internal", "app"), func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, found := appPkg["app"]
	if !found {
		t.Fatal("internal/app did not parse as package app")
	}

	// The exported interfaces of internal/app, and the Wire function that
	// takes each one.
	interfaces := map[string]bool{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				if _, isInterface := ts.Type.(*ast.InterfaceType); isInterface {
					interfaces[ts.Name.Name] = true
				}
			}
		}
	}
	if len(interfaces) == 0 {
		t.Fatal("found no exported interfaces in internal/app, so this test would assert nothing")
	}

	// A seam may have more than one consumer (FilePicker is wired into both
	// the transfer service and the audit export), so each interface maps to
	// every Wire function that takes it, and main.go must call them all.
	wiring := map[string][]string{} // interface name -> Wire function names
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || fn.Type.Params == nil {
				continue
			}
			for _, param := range fn.Type.Params.List {
				id, ok := param.Type.(*ast.Ident)
				if !ok || !interfaces[id.Name] {
					continue
				}
				wiring[id.Name] = append(wiring[id.Name], fn.Name.Name)
			}
		}
	}

	var unwirable []string
	for name := range interfaces {
		if len(wiring[name]) == 0 {
			unwirable = append(unwirable, name)
		}
	}
	sort.Strings(unwirable)
	if len(unwirable) != 0 {
		t.Errorf("internal/app exposes interfaces the shell cannot be given at all, so no build can supply them: %v", unwirable)
	}

	// What main.go actually calls.
	mainFile, err := parser.ParseFile(fset, filepath.Join(repoRoot, "main.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	called := map[string]bool{}
	ast.Inspect(mainFile, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "app" {
			called[sel.Sel.Name] = true
		}
		return true
	})
	if len(called) == 0 {
		t.Fatal("main.go calls nothing in internal/app, so this test would assert nothing")
	}

	var missing []string
	for name, wires := range wiring {
		for _, wire := range wires {
			if !called[wire] {
				missing = append(missing, name+" (wire it with app."+wire+")")
			}
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Errorf("main.go leaves a shell seam unwired, so the shipped build refuses what it offers: %v", missing)
	}
}
