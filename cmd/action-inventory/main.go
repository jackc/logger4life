// action-inventory inventories Logger4Life's core.Define declarations from
// source. It does not import or execute the application.
package main

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

type location struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

type entry struct {
	Name        string   `json:"name"`
	Symbol      string   `json:"symbol"`
	Description string   `json:"description"`
	Source      location `json:"source"`
}

type inventory struct {
	SchemaVersion int     `json:"schema_version"`
	Package       string  `json:"package"`
	Actions       []entry `json:"actions"`
}

func main() {
	root := flag.String("root", ".", "Logger4Life checkout root")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: action-inventory [-root checkout]")
		os.Exit(2)
	}
	if err := run(*root, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root string, out io.Writer) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pkgs, err := packages.Load(&packages.Config{
		Dir: root,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
	}, "./backend/core")
	if err != nil {
		return err
	}
	if len(pkgs) != 1 {
		return fmt.Errorf("expected one core package, found %d", len(pkgs))
	}
	pkg := pkgs[0]
	if len(pkg.Errors) != 0 {
		var errs []error
		for _, err := range pkg.Errors {
			errs = append(errs, errors.New(err.Error()))
		}
		return errors.Join(errs...)
	}
	doc, err := collect(pkg, root)
	if err != nil {
		return err
	}
	return json.MarshalWrite(out, doc, jsontext.WithIndent("  "), json.Deterministic(true))
}

func collect(pkg *packages.Package, root string) (inventory, error) {
	doc := inventory{SchemaVersion: 1, Package: pkg.PkgPath, Actions: []entry{}}
	define := pkg.Types.Scope().Lookup("Define")
	if define == nil {
		return doc, errors.New("core.Define was not found")
	}
	var problems []error
	names := map[string]location{}
	for _, file := range pkg.Syntax {
		// Associate direct package-level initializers with their Go variables.
		variables := map[ast.Expr]*ast.Ident{}
		for _, decl := range file.Decls {
			decl, ok := decl.(*ast.GenDecl)
			if !ok || decl.Tok != token.VAR {
				continue
			}
			for _, spec := range decl.Specs {
				spec := spec.(*ast.ValueSpec)
				if len(spec.Names) == len(spec.Values) {
					for i, value := range spec.Values {
						variables[ast.Unparen(value)] = spec.Names[i]
					}
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			fun := ast.Unparen(call.Fun)
			switch indexed := fun.(type) {
			case *ast.IndexExpr:
				fun = ast.Unparen(indexed.X)
			case *ast.IndexListExpr:
				fun = ast.Unparen(indexed.X)
			}
			ident, ok := fun.(*ast.Ident)
			if !ok || pkg.TypesInfo.Uses[ident] != define {
				return true
			}
			pos := pkg.Fset.PositionFor(call.Pos(), false)
			rel, err := filepath.Rel(root, pos.Filename)
			if err != nil {
				problems = append(problems, err)
				return true
			}
			source := location{File: filepath.ToSlash(rel), Line: pos.Line}
			item, err := readEntry(pkg, call, variables[call])
			if err != nil {
				problems = append(problems, fmt.Errorf("%s:%d: %w", source.File, source.Line, err))
				return true
			}
			item.Source = source
			if previous, exists := names[item.Name]; exists {
				problems = append(problems, fmt.Errorf("%s:%d: duplicate action %q (also at %s:%d)", source.File, source.Line, item.Name, previous.File, previous.Line))
			}
			names[item.Name] = source
			doc.Actions = append(doc.Actions, item)
			return true
		})
	}
	if len(problems) != 0 {
		return doc, errors.Join(problems...)
	}
	if len(doc.Actions) == 0 {
		return doc, errors.New("no supported core.Define declarations found")
	}
	slices.SortFunc(doc.Actions, func(a, b entry) int { return strings.Compare(a.Name, b.Name) })
	return doc, nil
}

func readEntry(pkg *packages.Package, call *ast.CallExpr, variable *ast.Ident) (entry, error) {
	var item entry
	if variable == nil || len(call.Args) != 1 {
		return item, errors.New("unsupported Define call: expected a direct package-level variable initializer")
	}
	literal, ok := ast.Unparen(call.Args[0]).(*ast.CompositeLit)
	if !ok {
		return item, errors.New("unsupported Define argument: expected an ActionDef literal")
	}
	item.Symbol = variable.Name
	for _, elt := range literal.Elts {
		field, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return item, errors.New("unsupported ActionDef: expected named fields")
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || (key.Name != "Name" && key.Name != "Description") {
			continue
		}
		value := pkg.TypesInfo.Types[field.Value].Value
		if value == nil || value.Kind() != constant.String {
			return item, fmt.Errorf("unsupported %s: expected a compile-time string constant", key.Name)
		}
		if key.Name == "Name" {
			item.Name = constant.StringVal(value)
		} else {
			item.Description = constant.StringVal(value)
		}
	}
	if item.Name == "" {
		return item, errors.New("action name is missing or empty")
	}
	return item, nil
}
