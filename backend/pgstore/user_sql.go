package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/logger4life/backend/core"
	pgsqlarbiter "github.com/jackc/pgsqlarbiter-go"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	userSQLStatementTimeout = "5s"
	userSQLIdleTimeout      = "10s"
	userSQLMaxRows          = 1000
	userSQLMaxResultBytes   = 1 << 20
)

func newUserSQLArbiter() *pgsqlarbiter.Arbiter {
	return &pgsqlarbiter.Arbiter{
		AllowedStatementTypes: []pgsqlarbiter.StatementType{pgsqlarbiter.StatementSelect},
		AllowedTables:         []string{"logs", "log_entries"},
	}
}

func userSQLFailure(kind core.UserSQLFailureKind, message string) error {
	return &core.UserSQLFailure{Kind: kind, Message: message}
}

func translateArbiterError(err error) error {
	var multiple *pgsqlarbiter.MultipleStatementsError
	if errors.As(err, &multiple) {
		return userSQLFailure(core.UserSQLRejected, "multiple statements are not allowed")
	}
	var disallowed *pgsqlarbiter.DisallowedStatementError
	if errors.As(err, &disallowed) {
		return userSQLFailure(core.UserSQLRejected, "only SELECT statements are allowed")
	}
	// Lexing and parsing diagnostics may contain unstable implementation
	// details. They are intentionally collapsed to a fixed public message.
	return userSQLFailure(core.UserSQLRejected, "invalid SQL query")
}

func translateArbiterVerdict(v *pgsqlarbiter.Verdict) error {
	parts := []string{}
	if v.DisallowedStatementType {
		parts = append(parts, "only SELECT statements are allowed")
	}
	if len(v.DisallowedTables) > 0 {
		parts = append(parts, "tables not allowed: "+strings.Join(v.DisallowedTables, ", "))
	}
	if len(v.DisallowedFunctions) > 0 {
		parts = append(parts, "functions not allowed: "+strings.Join(v.DisallowedFunctions, ", "))
	}
	if len(parts) == 0 {
		return userSQLFailure(core.UserSQLRejected, "query rejected")
	}
	return userSQLFailure(core.UserSQLRejected, strings.Join(parts, "; "))
}

func translateUserSQLPostgresError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "57014":
		return userSQLFailure(core.UserSQLTimedOut, "query timed out")
	case "25006":
		return userSQLFailure(core.UserSQLRejected, "writes are not allowed in this query")
	case "42601":
		return userSQLFailure(core.UserSQLRejected, "invalid SQL query")
	default:
		// SQLSTATE is a stable category and does not contain relation names,
		// values, server paths, or other details from PostgreSQL's message.
		return userSQLFailure(core.UserSQLFailed, fmt.Sprintf("query failed (SQLSTATE %s)", pgErr.Code))
	}
}

func userSQLTypeName(typeMap *pgtype.Map, oid uint32) string {
	if dataType, ok := typeMap.TypeForOID(oid); ok {
		return dataType.Name
	}
	return fmt.Sprintf("oid %d", oid)
}

// ExecuteUserSQL applies the complete constrained-query boundary: static SQL
// analysis, a read-only transaction, a restricted PostgreSQL role, per-user
// view context, execution timeouts, and bounded result collection.
func (s *Store) ExecuteUserSQL(ctx context.Context, userID, query string) (core.UserSQLResult, error) {
	verdict, err := s.userSQLArbiter.Judge(query)
	if err != nil {
		return core.UserSQLResult{}, translateArbiterError(err)
	}
	if !verdict.Allowed {
		return core.UserSQLResult{}, translateArbiterVerdict(verdict)
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return core.UserSQLResult{}, err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return core.UserSQLResult{}, err
	}
	defer tx.Rollback(ctx)

	setup := []string{
		"SET LOCAL statement_timeout = '" + userSQLStatementTimeout + "'",
		"SET LOCAL idle_in_transaction_session_timeout = '" + userSQLIdleTimeout + "'",
	}
	for _, statement := range setup {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return core.UserSQLResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", userID); err != nil {
		return core.UserSQLResult{}, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE logger4life_sql_user"); err != nil {
		return core.UserSQLResult{}, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO sql_query"); err != nil {
		return core.UserSQLResult{}, err
	}

	startedAt := time.Now()
	queryCtx, cancelQuery := context.WithCancel(ctx)
	defer cancelQuery()
	rows, err := tx.Query(queryCtx, query,
		pgx.QueryExecModeExec,
		pgx.QueryResultFormats{pgx.TextFormatCode},
	)
	if err != nil {
		return core.UserSQLResult{}, translateUserSQLPostgresError(err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	columns := make([]core.UserSQLColumn, len(fields))
	typeMap := conn.Conn().TypeMap()
	for i, field := range fields {
		columns[i] = core.UserSQLColumn{Name: string(field.Name), DataType: userSQLTypeName(typeMap, field.DataTypeOID)}
	}

	resultRows := make([][]*string, 0)
	resultBytes := 0
	truncated := false
	for rows.Next() {
		if len(resultRows) >= userSQLMaxRows {
			truncated = true
			break
		}
		rawValues := rows.RawValues()
		rowBytes := 0
		for _, value := range rawValues {
			rowBytes += len(value)
		}
		if resultBytes+rowBytes > userSQLMaxResultBytes {
			truncated = true
			break
		}

		row := make([]*string, len(rawValues))
		for i, value := range rawValues {
			if value == nil {
				continue
			}
			text := string(value)
			row[i] = &text
		}
		resultRows = append(resultRows, row)
		resultBytes += rowBytes
	}

	if truncated {
		// Stop PostgreSQL producing rows that cannot be returned. The expected
		// cancellation error is ignored because the bounded prefix is valid.
		cancelQuery()
		rows.Close()
	} else if err := rows.Err(); err != nil {
		return core.UserSQLResult{}, translateUserSQLPostgresError(err)
	}

	return core.UserSQLResult{
		Columns: columns, Rows: resultRows, RowCount: len(resultRows),
		Truncated: truncated, ElapsedMs: time.Since(startedAt).Milliseconds(),
	}, nil
}
