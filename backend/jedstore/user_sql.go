package jedstore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	jed "github.com/jackc/jed/impl/go"
	"github.com/jackc/logger4life/backend/core"
	pgsqlarbiter "github.com/jackc/pgsqlarbiter-go"
)

const (
	userSQLStatementTimeout = 5 * time.Second
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

func translateUserSQLJedError(err error) error {
	var engineErr *jed.EngineError
	if !errors.As(err, &engineErr) {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return userSQLFailure(core.UserSQLTimedOut, "query timed out")
		}
		return err
	}
	switch engineErr.Code() {
	case "57014", "54P01", "54P02":
		return userSQLFailure(core.UserSQLTimedOut, "query timed out")
	case "25006", "42501":
		return userSQLFailure(core.UserSQLRejected, "writes are not allowed in this query")
	case "42601":
		return userSQLFailure(core.UserSQLRejected, "invalid SQL query")
	default:
		return userSQLFailure(core.UserSQLFailed, fmt.Sprintf("query failed (SQLSTATE %s)", engineErr.Code()))
	}
}

func userSQLCell(value any) *string {
	if value == nil {
		return nil
	}
	var text string
	switch v := value.(type) {
	case jed.Value:
		if v.Kind == jed.ValNull {
			return nil
		}
		text = v.Render()
	case []byte:
		text = `\x` + hex.EncodeToString(v)
	case time.Time:
		text = v.UTC().Format(time.RFC3339Nano)
	default:
		text = fmt.Sprint(v)
	}
	return &text
}

func textArraySQLLiteral(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		value = strings.ReplaceAll(value, `\`, `\\`)
		value = strings.ReplaceAll(value, `"`, `\"`)
		quoted[i] = `"` + value + `"`
	}
	array := "{" + strings.Join(quoted, ",") + "}"
	return "'" + strings.ReplaceAll(array, "'", "''") + "'"
}

// ExecuteUserSQL uses a separate read-only jed session. Its host privilege
// envelope grants SELECT only on two session-local filtered projections.
func (s *Store) ExecuteUserSQL(ctx context.Context, userID, query string) (core.UserSQLResult, error) {
	verdict, err := s.userSQLArbiter.Judge(query)
	if err != nil {
		return core.UserSQLResult{}, translateArbiterError(err)
	}
	if !verdict.Allowed {
		return core.UserSQLResult{}, translateArbiterVerdict(verdict)
	}

	allowDDL := false
	allowTempDDL := true
	session := s.db.Session(jed.SessionOptions{
		AllowDDL:     &allowDDL,
		AllowTempDDL: &allowTempDDL,
	})
	defer session.Close()

	// jed has no persistent views. Build the same two projections as
	// session-local temp tables while the session is trusted, then remove all
	// default table privileges before executing the outside query.
	setup := []string{
		`CREATE TEMP TABLE logs (
			id text, name varchar(100), fields jsonb, created_at timestamptz,
			updated_at timestamptz, shared_with text[]
		)`,
		`CREATE TEMP TABLE log_entries (
			id text, log_id text, user_id text, user_username varchar(30), fields jsonb,
			occurred_at timestamptz, created_at timestamptz, updated_at timestamptz
		)`,
	}
	for i, statement := range setup {
		if _, err := session.Exec(ctx, statement); err != nil {
			return core.UserSQLResult{}, fmt.Errorf("prepare user SQL table %d: %w", i+1, err)
		}
	}

	// Populate both projections in one database snapshot. jed does not yet
	// implement array_agg, so the owner-only shared_with array is assembled
	// from the member rows below.
	err = session.Update(func(tx *jed.Transaction) error {
		if _, err := tx.Exec(ctx, `INSERT INTO logs(id,name,fields,created_at,updated_at)
			SELECT l.id, l.name, l.fields, l.created_at, l.updated_at
			FROM all_logs l
			WHERE l.user_id = $1 OR EXISTS (
				SELECT 1 FROM log_shares ls WHERE ls.log_id = l.id AND ls.user_id = $1
			)`, userID); err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `SELECT l.id,u.username
			FROM all_logs l
			LEFT JOIN log_shares ls ON ls.log_id=l.id
			LEFT JOIN users u ON u.id=ls.user_id
			WHERE l.user_id=$1
			ORDER BY l.id,u.username`, userID)
		if err != nil {
			return err
		}
		members := map[string][]string{}
		for rows.Next() {
			var logID string
			var username jed.Null[string]
			if err := rows.Scan(&logID, &username); err != nil {
				rows.Close()
				return err
			}
			if _, ok := members[logID]; !ok {
				members[logID] = []string{}
			}
			if username.Valid {
				members[logID] = append(members[logID], username.Val)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for logID, usernames := range members {
			// Array-valued parameters are not implemented by jed yet. This
			// literal is assembled only from stored usernames and is escaped
			// both as PostgreSQL array text and as a SQL string literal.
			if _, err := tx.Exec(ctx, `UPDATE logs SET shared_with=`+textArraySQLLiteral(usernames)+` WHERE id=$1`, logID); err != nil {
				return err
			}
		}

		_, err = tx.Exec(ctx, `INSERT INTO log_entries
			SELECT le.id, le.log_id, le.user_id, u.username, le.fields,
			       le.occurred_at, le.created_at, le.updated_at
			FROM all_log_entries le JOIN users u ON u.id = le.user_id
			WHERE le.log_id IN (SELECT id FROM logs)`)
		return err
	})
	if err != nil {
		return core.UserSQLResult{}, fmt.Errorf("prepare user SQL views: %w", err)
	}
	session.Privileges().SetDefaultTable(jed.PrivSetEmpty)
	session.Grant(jed.PrivSetEmpty.With(jed.PrivSelect), "logs")
	session.Grant(jed.PrivSetEmpty.With(jed.PrivSelect), "log_entries")
	session.SetAllowTempDDL(false)

	queryCtx, cancel := context.WithTimeout(ctx, userSQLStatementTimeout)
	defer cancel()
	startedAt := time.Now()
	var result core.UserSQLResult
	err = session.View(func(tx *jed.Transaction) error {
		rows, err := tx.Query(queryCtx, query)
		if err != nil {
			return err
		}
		defer rows.Close()

		names, types := rows.ColumnNames(), rows.ColumnTypes()
		result.Columns = make([]core.UserSQLColumn, len(names))
		for i := range names {
			result.Columns[i] = core.UserSQLColumn{Name: names[i], DataType: types[i]}
		}
		result.Rows = make([][]*string, 0)
		resultBytes := 0
		for rows.Next() {
			if len(result.Rows) >= userSQLMaxRows {
				result.Truncated = true
				break
			}
			values, err := rows.Values()
			if err != nil {
				return err
			}
			row := make([]*string, len(values))
			rowBytes := 0
			for i, value := range values {
				row[i] = userSQLCell(value)
				if row[i] != nil {
					rowBytes += len(*row[i])
				}
			}
			if resultBytes+rowBytes > userSQLMaxResultBytes {
				result.Truncated = true
				break
			}
			result.Rows = append(result.Rows, row)
			resultBytes += rowBytes
		}
		return rows.Err()
	})
	if err != nil {
		return core.UserSQLResult{}, translateUserSQLJedError(err)
	}
	result.RowCount = len(result.Rows)
	result.ElapsedMs = time.Since(startedAt).Milliseconds()
	return result, nil
}
