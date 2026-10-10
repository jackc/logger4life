package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"golang.org/x/tools/go/packages"
)

func fixture(t *testing.T, declarations string) inventory {
	t.Helper()
	doc, err := collectFixture(t, declarations)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func collectFixture(t *testing.T, declarations string) (inventory, error) {
	t.Helper()
	const prefix = `package core
type definition[T any] struct { Name, Description string }
func Define[T any](d definition[T]) *T { panic("must not execute") }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "/repo/backend/core/actions.go", prefix+declarations, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	config := types.Config{}
	pkg, err := config.Check("example/core", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	return collect(&packages.Package{
		PkgPath: pkg.Path(), Fset: fset, Syntax: []*ast.File{file}, Types: pkg, TypesInfo: info,
	}, "/repo")
}

func TestInventoryDeclarations(t *testing.T) {
	doc := fixture(t, `const prefix = "a"
var Z = Define(definition[int]{Name: "z", Description: "last"})
var A = Define[int](definition[int]{Name: prefix + "ction", Description: "first"})
func unrelated() {
    Define := func(d definition[int]) int { return 0 }
    _ = Define(definition[int]{Name: "not_an_action"})
}
`)
	if len(doc.Actions) != 2 || doc.Actions[0].Name != "action" || doc.Actions[1].Name != "z" {
		t.Fatalf("unexpected inventory: %#v", doc)
	}
	want := entry{Name: "action", Symbol: "A", Description: "first", Source: location{File: "backend/core/actions.go", Line: 6}}
	if doc.Actions[0] != want {
		t.Fatalf("entry = %#v, want %#v", doc.Actions[0], want)
	}
}

func TestInventoryRejectsUnsupportedDeclarations(t *testing.T) {
	for _, tc := range []struct{ name, source, message string }{
		{"computed name", `func name() string { return "dynamic" }; var A = Define(definition[int]{Name: name()})`, "compile-time string constant"},
		{"indirect definition", `var d = definition[int]{Name: "indirect"}; var A = Define(d)`, "expected an ActionDef literal"},
		{"runtime registration", `func register() { Define(definition[int]{Name: "runtime"}) }`, "package-level variable initializer"},
		{"duplicate", `var A = Define(definition[int]{Name: "same"}); var B = Define(definition[int]{Name: "same"})`, `duplicate action "same"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := collectFixture(t, tc.source)
			if err == nil || !strings.Contains(err.Error(), tc.message) || !strings.Contains(err.Error(), "backend/core/actions.go:4:") {
				t.Fatalf("error = %v; want located diagnostic containing %q", err, tc.message)
			}
		})
	}
}

func TestInventoryMatchesProductionCatalog(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(root, &out); err != nil {
		t.Fatal(err)
	}
	var doc inventory
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	catalog := core.Catalog()
	if doc.SchemaVersion != 1 || doc.Package != "github.com/jackc/logger4life/backend/core" || len(doc.Actions) != len(catalog) {
		t.Fatalf("source inventory has %d entries, runtime catalog has %d; document = %#v", len(doc.Actions), len(catalog), doc)
	}
	for i, action := range catalog {
		item := doc.Actions[i]
		if item.Name != action.Name() || item.Description != action.Description() {
			t.Errorf("entry %d = %#v, want %s: %s", i, item, action.Name(), action.Description())
		}
		if strings.HasSuffix(item.Source.File, "_test.go") || filepath.IsAbs(item.Source.File) || item.Source.Line < 1 {
			t.Errorf("invalid production source location: %#v", item.Source)
		}
	}
}
