package storetest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
)

// RunSQLSchemaStore checks the port that describes the read-only views the
// SQL feature exposes. It takes no user: the shape of the schema is the same
// for everyone, and it is what each user's own rows are filtered through.
func RunSQLSchemaStore(t *testing.T, ports Ports) {
	views, err := ports.ListSQLSchemaViews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) == 0 {
		t.Fatal("ListSQLSchemaViews returned nothing; the SQL feature has no schema to describe")
	}

	// The description is what a person — or a model, through MCP — reads to
	// write a query against these views, so a view with no columns or a
	// column with no type is not a usable answer.
	byName := map[string]*core.SQLSchemaView{}
	for _, view := range views {
		if view.Name == "" {
			t.Errorf("a view came back unnamed: %#v", view)
		}
		if len(view.Columns) == 0 {
			t.Errorf("view %q came back with no columns", view.Name)
		}
		for _, column := range view.Columns {
			if column.Name == "" || column.DataType == "" {
				t.Errorf("view %q has a column with no name or type: %#v", view.Name, column)
			}
		}
		byName[view.Name] = view
	}

	// These two are the schema the feature is built around.
	for _, name := range []string{"logs", "log_entries"} {
		view, ok := byName[name]
		if !ok {
			t.Errorf("the schema does not describe %q", name)
			continue
		}
		if view.Comment == nil || *view.Comment == "" {
			t.Errorf("view %q has no comment; the comment is how a caller learns what it holds", name)
		}
	}
}

// RunUserSQLExecutor checks the port that runs SQL a user wrote. It is the one
// place in the app where an outside string reaches the database, so its whole
// contract is a boundary: what SQL is allowed to run, what rows it may see,
// and what a failure is permitted to say.
func RunUserSQLExecutor(t *testing.T, ports Ports) {
	ctx := context.Background()

	t.Run("returns a user's own rows with their columns", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins", doseField())
		if _, err := ports.CreateLogEntry(ctx, newRowID(), owner.ID, log.ID, map[string]any{"dose": float64(500)}, newOccurredAt()); err != nil {
			t.Fatal(err)
		}

		result, err := ports.ExecuteUserSQL(ctx, owner.ID, "SELECT name FROM logs ORDER BY name")
		if err != nil {
			t.Fatal(err)
		}
		if result.RowCount != 1 || len(result.Rows) != 1 {
			t.Fatalf("result = %#v, want the one log", result)
		}
		if len(result.Columns) != 1 || result.Columns[0].Name != "name" || result.Columns[0].DataType == "" {
			t.Errorf("columns = %#v, want the selected column described", result.Columns)
		}
		if result.Rows[0][0] == nil || *result.Rows[0][0] != "Vitamins" {
			t.Errorf("row = %#v, want the log's name", result.Rows[0])
		}
		if result.Truncated {
			t.Error("a single-row result came back marked truncated")
		}
	})

	// A NULL has to be distinguishable from an empty string, which is why a
	// cell is a pointer rather than a string. Collapsing the two would make a
	// missing value and a blank one look alike in every result a user reads.
	t.Run("distinguishes a null cell from an empty one", func(t *testing.T) {
		owner := newUser(t, ports)
		newLog(t, ports, owner.ID, "Vitamins")

		result, err := ports.ExecuteUserSQL(ctx, owner.ID, "SELECT NULL::text AS missing, '' AS blank FROM logs")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != 1 || len(result.Rows[0]) != 2 {
			t.Fatalf("result = %#v, want one row of two cells", result.Rows)
		}
		if result.Rows[0][0] != nil {
			t.Errorf("a NULL came back as %q, want nil", *result.Rows[0][0])
		}
		if result.Rows[0][1] == nil {
			t.Error("an empty string came back as nil, which is how a NULL is reported")
		} else if *result.Rows[0][1] != "" {
			t.Errorf("an empty string came back as %q", *result.Rows[0][1])
		}
	})

	// The load-bearing case. Every user runs queries against the same views,
	// so the filtering is what keeps one account's history out of another's
	// results — there is no other check between the query and the tables.
	t.Run("shows a user only rows they own or were shared", func(t *testing.T) {
		owner := newUser(t, ports)
		stranger := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Private", doseField())
		if _, err := ports.CreateLogEntry(ctx, newRowID(), owner.ID, log.ID, map[string]any{"dose": float64(500)}, newOccurredAt()); err != nil {
			t.Fatal(err)
		}

		for _, query := range []string{
			"SELECT count(*) FROM logs",
			"SELECT count(*) FROM log_entries",
		} {
			result, err := ports.ExecuteUserSQL(ctx, stranger.ID, query)
			if err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			if len(result.Rows) != 1 || len(result.Rows[0]) != 1 || result.Rows[0][0] == nil {
				t.Errorf("%s as a stranger returned %#v, want one count", query, result.Rows)
				continue
			}
			if count := *result.Rows[0][0]; count != "0" {
				t.Errorf("%s as a stranger counted %s, want 0: another user's rows are visible", query, count)
			}
		}

		// Naming the log directly must not reach it either.
		result, err := ports.ExecuteUserSQL(ctx, stranger.ID, "SELECT name FROM logs WHERE name = 'Private'")
		if err != nil {
			t.Fatal(err)
		}
		if result.RowCount != 0 {
			t.Errorf("a stranger selected another user's log by name: %#v", result.Rows)
		}

		// Sharing is the one way across, and it works.
		token := []byte("sqltoken-" + owner.Username)
		if err := ports.CreateShareToken(ctx, owner.ID, log.ID, token); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.JoinSharedLog(ctx, newRowID(), stranger.ID, token); err != nil {
			t.Fatal(err)
		}
		shared, err := ports.ExecuteUserSQL(ctx, stranger.ID, "SELECT name FROM logs")
		if err != nil {
			t.Fatal(err)
		}
		if shared.RowCount != 1 {
			t.Errorf("a shared member sees %#v, want the shared log", shared.Rows)
		}

		ownerView, err := ports.ExecuteUserSQL(ctx, owner.ID, "SELECT shared_with FROM logs")
		if err != nil {
			t.Fatal(err)
		}
		wantMembers := "{" + stranger.Username + "}"
		if len(ownerView.Rows) != 1 || ownerView.Rows[0][0] == nil || *ownerView.Rows[0][0] != wantMembers {
			t.Errorf("owner shared_with = %#v, want %q", ownerView.Rows, wantMembers)
		}
		memberView, err := ports.ExecuteUserSQL(ctx, stranger.ID, "SELECT shared_with FROM logs")
		if err != nil {
			t.Fatal(err)
		}
		if len(memberView.Rows) != 1 || memberView.Rows[0][0] != nil {
			t.Errorf("member shared_with = %#v, want NULL", memberView.Rows)
		}
	})

	// Only SELECT, and only over the two views. Anything else is refused
	// before it reaches the database, and the refusal is a UserSQLFailure the
	// adapter is allowed to show the caller.
	t.Run("refuses anything but a select over the exposed views", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins")

		refused := []struct {
			what  string
			query string
		}{
			{"insert", "INSERT INTO logs (name) VALUES ('x')"},
			{"update", "UPDATE logs SET name = 'x'"},
			{"delete", "DELETE FROM log_entries"},
			{"drop", "DROP TABLE logs"},
			{"a table outside the views", "SELECT * FROM users"},
			{"the sessions table", "SELECT * FROM sessions"},
			{"stacked statements", "SELECT 1 FROM logs; DROP TABLE logs"},
			{"nonsense", "NOT SQL AT ALL"},
		}
		for _, c := range refused {
			_, err := ports.ExecuteUserSQL(ctx, owner.ID, c.query)
			var failure *core.UserSQLFailure
			if !errors.As(err, &failure) {
				t.Errorf("%s returned %T %v, want a UserSQLFailure", c.what, err, err)
				continue
			}
			if failure.Kind != core.UserSQLRejected {
				t.Errorf("%s was reported as %q, want %q", c.what, failure.Kind, core.UserSQLRejected)
			}
			assertSafeMessage(t, c.what, failure.Message)
		}

		// The log survives every one of them.
		if _, err := ports.GetLog(ctx, owner.ID, log.ID); err != nil {
			t.Errorf("a refused statement still changed something: %v", err)
		}
	})

	// A failure message is handed to the caller verbatim, so it must never
	// carry what the database said — relation names, values, or server paths.
	t.Run("reports a failing query without quoting the database", func(t *testing.T) {
		owner := newUser(t, ports)
		newLog(t, ports, owner.ID, "Failure")

		_, err := ports.ExecuteUserSQL(ctx, owner.ID, "SELECT 1/0 FROM logs")
		var failure *core.UserSQLFailure
		if !errors.As(err, &failure) {
			t.Fatalf("division by zero returned %v, want a safe UserSQLFailure", err)
		}
		assertSafeMessage(t, "a failing query", failure.Message)
	})

	// A result that would not fit comes back as a bounded prefix that says so,
	// rather than as an error or an unbounded response.
	t.Run("bounds a large result and says it did", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Bulk")
		for range 3 {
			if _, err := ports.CreateLogEntry(ctx, newRowID(), owner.ID, log.ID, map[string]any{}, newOccurredAt()); err != nil {
				t.Fatal(err)
			}
		}

		result, err := ports.ExecuteUserSQL(ctx, owner.ID,
			"SELECT a.id FROM log_entries a, log_entries b, log_entries c, log_entries d, log_entries e, log_entries f, log_entries g ORDER BY a.id, b.id, c.id, d.id, e.id, f.id, g.id")
		if err != nil {
			t.Fatal(err)
		}
		if !result.Truncated {
			t.Errorf("a %d-row result came back whole; large results must be bounded", result.RowCount)
		}
		if result.RowCount == 0 {
			t.Error("a truncated result came back empty, not as a bounded prefix")
		}
	})
}

// assertSafeMessage checks that a message meant for a caller does not quote
// the database back at them.
func assertSafeMessage(t *testing.T, what, message string) {
	t.Helper()
	if message == "" {
		t.Errorf("%s produced an empty message; the caller is told nothing", what)
		return
	}
	for _, leak := range []string{"ERROR:", "SQLSTATE ", "pq:", "pgx", "relation \"", "LINE ", "/var/", "postgres://"} {
		if strings.Contains(message, leak) && !strings.HasPrefix(message, "query failed (SQLSTATE ") {
			t.Errorf("%s reported %q, which quotes the database back to the caller", what, message)
		}
	}
}
