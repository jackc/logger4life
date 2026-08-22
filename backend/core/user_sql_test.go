package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeUserSQLExecutor struct {
	userID string
	query  string
	result UserSQLResult
	err    error
	calls  int
}

func (e *fakeUserSQLExecutor) ExecuteUserSQL(_ context.Context, userID, query string) (UserSQLResult, error) {
	e.calls++
	e.userID = userID
	e.query = query
	return e.result, e.err
}

func TestExecuteUserSQLUsesTrustedUserAndNormalizesQuery(t *testing.T) {
	want := UserSQLResult{
		Columns: []UserSQLColumn{{Name: "name", DataType: "text"}},
		Rows:    [][]*string{{stringPointer("Vitamins")}}, RowCount: 1,
	}
	executor := &fakeUserSQLExecutor{result: want}
	app := New(Config{UserSQL: executor})

	got, err := ExecuteUserSQL.Call(WithUserID(context.Background(), "user-1"), app, ExecuteUserSQLParams{
		Query: "  SELECT name FROM logs  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || executor.userID != "user-1" || executor.query != "SELECT name FROM logs" {
		t.Fatalf("executor call = %d, %q, %q", executor.calls, executor.userID, executor.query)
	}
	if got.RowCount != want.RowCount || got.Columns[0] != want.Columns[0] || *got.Rows[0][0] != "Vitamins" {
		t.Fatalf("ExecuteUserSQL() = %#v", got)
	}
	if action, ok := Lookup("execute_user_sql"); !ok || action != ExecuteUserSQL {
		t.Fatal("execute_user_sql is missing from the action catalog")
	}
}

func TestExecuteUserSQLRequiresAuthenticationBeforeCallingPort(t *testing.T) {
	executor := &fakeUserSQLExecutor{}
	app := New(Config{UserSQL: executor})
	_, err := ExecuteUserSQL.Call(context.Background(), app, ExecuteUserSQLParams{Query: "SELECT 1"})
	if !errors.Is(err, ErrUnauthenticated) || executor.calls != 0 {
		t.Fatalf("ExecuteUserSQL() error = %v; calls = %d", err, executor.calls)
	}
}

func TestExecuteUserSQLValidatesQuery(t *testing.T) {
	executor := &fakeUserSQLExecutor{}
	app := New(Config{UserSQL: executor})
	ctx := WithUserID(context.Background(), "user-1")
	for _, query := range []string{"   ", strings.Repeat("x", MaxUserSQLQueryLength+1)} {
		_, err := ExecuteUserSQL.Call(ctx, app, ExecuteUserSQLParams{Query: query})
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("query length %d: error = %T %v, want ValidationError", len(query), err, err)
		}
	}
	if executor.calls != 0 {
		t.Fatalf("invalid queries reached executor %d times", executor.calls)
	}
}

func TestExecuteUserSQLPreservesSafeAndInternalPortErrors(t *testing.T) {
	executor := &fakeUserSQLExecutor{}
	app := New(Config{UserSQL: executor})
	ctx := WithUserID(context.Background(), "user-1")

	safe := &UserSQLFailure{Kind: UserSQLRejected, Message: "tables not allowed: secrets"}
	executor.err = safe
	if _, err := ExecuteUserSQL.Call(ctx, app, ExecuteUserSQLParams{Query: "SELECT * FROM secrets"}); err != safe {
		t.Fatalf("safe error = %T %v", err, err)
	}

	internal := errors.New("database host is secret.internal")
	executor.err = internal
	if _, err := ExecuteUserSQL.Call(ctx, app, ExecuteUserSQLParams{Query: "SELECT 1"}); err != internal {
		t.Fatalf("internal error = %T %v", err, err)
	}
}
