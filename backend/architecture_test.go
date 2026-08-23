package backend

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// postgreSQLPackages are the import paths that let a file speak PostgreSQL.
// SQL cannot be executed without one of them, so restricting these imports is
// what keeps application SQL inside the approved infrastructure packages.
var postgreSQLPackages = []string{
	"github.com/jackc/pgx/",
}

// postgreSQLAllowed are the only non-test files permitted to import them:
// the store implementations, and the composition root that opens the pool
// and builds the liveness probe.
var postgreSQLAllowed = []string{
	"pgstore/",
	"server/server.go",
}

var jedPackages = []string{
	"github.com/jackc/jed/",
}

var jedAllowed = []string{
	"jedstore/",
	"server/server.go",
}

var sqlArbiterPackages = []string{
	"github.com/jackc/pgsqlarbiter-go",
}

var sqlArbiterAllowed = []string{
	"pgstore/",
	"jedstore/",
}

// pureDependencyRules keep the domain core independent of transport and
// persistence. Each package may not import anything with these prefixes.
var pureDependencyRules = map[string][]string{
	"core": {
		"net/http",
		"github.com/go-chi/",
		"github.com/spf13/cobra",
		"github.com/jackc/pgx/",
		"github.com/jackc/jed/",
		"github.com/jackc/pgsqlarbiter-go",
		"github.com/jackc/logger4life/backend/pgstore",
		"github.com/jackc/logger4life/backend/jedstore",
		"github.com/jackc/logger4life/backend/dualstore",
		"github.com/jackc/logger4life/backend/server",
	},
	"domain": {
		"net/http",
		"github.com/go-chi/",
		"github.com/spf13/cobra",
		"github.com/jackc/pgx/",
		"github.com/jackc/jed/",
		"github.com/jackc/pgsqlarbiter-go",
		"github.com/jackc/logger4life/backend/core",
		"github.com/jackc/logger4life/backend/pgstore",
		"github.com/jackc/logger4life/backend/jedstore",
		"github.com/jackc/logger4life/backend/dualstore",
		"github.com/jackc/logger4life/backend/server",
	},
}

// TestArchitecturalBoundaries fails when a file imports something its layer is
// not allowed to depend on. It walks the backend tree rather than a fixed list
// so a newly added file is covered without being registered anywhere.
func TestArchitecturalBoundaries(t *testing.T) {
	fset := token.NewFileSet()

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel := filepath.ToSlash(path)

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}

			// Tests reach for the test database directly; the rule covers
			// the code that ships.
			if !strings.HasSuffix(rel, "_test.go") && hasAnyPrefix(imported, postgreSQLPackages) && !hasAnyPrefix(rel, postgreSQLAllowed) {
				t.Errorf("%s imports %q; PostgreSQL belongs in %s", rel, imported, strings.Join(postgreSQLAllowed, " or "))
			}
			if !strings.HasSuffix(rel, "_test.go") && hasAnyPrefix(imported, jedPackages) && !hasAnyPrefix(rel, jedAllowed) {
				t.Errorf("%s imports %q; jed belongs in %s", rel, imported, strings.Join(jedAllowed, " or "))
			}
			if !strings.HasSuffix(rel, "_test.go") && hasAnyPrefix(imported, sqlArbiterPackages) && !hasAnyPrefix(rel, sqlArbiterAllowed) {
				t.Errorf("%s imports %q; user SQL arbitration belongs in %s", rel, imported, strings.Join(sqlArbiterAllowed, " or "))
			}

			pkg, _, _ := strings.Cut(rel, "/")
			for _, banned := range pureDependencyRules[pkg] {
				if strings.HasPrefix(imported, banned) {
					t.Errorf("%s imports %q; %s must not depend on transport or persistence", rel, imported, pkg)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
