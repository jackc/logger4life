package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the database seam that lets Logger4Life run on either PostgreSQL (via pgx)
// or jed (https://github.com/jackc/jed), selected at startup by the DATABASE_URL
// scheme. It is intentionally a strict subset of the pgx API we actually use, so:
//
//   - the PostgreSQL implementation is a trivial pass-through (pgxDB below), and
//   - a future jed implementation only has to satisfy this small surface.
//
// Method names and signatures mirror pgx (including the leading context.Context,
// which a jed backend may ignore) so the ~150 existing call sites are unchanged
// apart from the parameter type.
//
// The common error vocabulary is pgx's: callers test no-rows with
// errors.Is(err, pgx.ErrNoRows) and unique violations via *pgconn.PgError code
// 23505. A jed backend must translate its native errors into those sentinels so
// the shared handler logic keeps working. See CONVERSION_STATUS.md.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) Row
	Exec(ctx context.Context, sql string, args ...any) (CommandTag, error)
	Begin(ctx context.Context) (Tx, error)
}

// Row is a single-row result; Scan binds the row's columns into dest.
type Row interface {
	Scan(dest ...any) error
}

// Rows is a forward-only cursor over a multi-row result.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}

// CommandTag reports the effect of an Exec (e.g. rows affected).
type CommandTag interface {
	RowsAffected() int64
}

// Tx is an in-progress transaction. Commit/Rollback take a context to match pgx.
type Tx interface {
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) Row
	Exec(ctx context.Context, sql string, args ...any) (CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// OpenDB connects to the database named by databaseURL and returns the DB seam.
//
// It also returns the concrete *pgxpool.Pool when (and only when) the PostgreSQL
// backend is selected; it is nil for any non-PostgreSQL backend. PostgreSQL-only
// features (the user SQL-query feature, MCP, and OAuth) take the concrete pool and
// are mounted only when it is non-nil. Callers must Close the returned pool.
func OpenDB(ctx context.Context, databaseURL string) (DB, *pgxpool.Pool, error) {
	if isJedURL(databaseURL) {
		// The jed backend is not wired yet: jed currently offers either a durable
		// but non-goroutine-safe *Database or a goroutine-safe but in-memory-only
		// SharedDB, with no durable+concurrent handle for a concurrent HTTP server
		// (CONVERSION_STATUS.md, infelicity #1). Awaiting an upstream jed fix.
		return nil, nil, fmt.Errorf("jed backend (%q) is not implemented yet; see CONVERSION_STATUS.md", databaseURL)
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to connect to database: %w", err)
	}
	return pgxDB{pool: pool}, pool, nil
}

// isJedURL reports whether databaseURL selects the jed backend. jed databases are
// named with a jed: scheme (e.g. jed:///var/lib/logger4life/data.jed); everything
// else (postgres://, postgresql://) selects PostgreSQL.
func isJedURL(databaseURL string) bool {
	return strings.HasPrefix(databaseURL, "jed:")
}

// pgxDB is the PostgreSQL implementation of DB: a thin adapter over *pgxpool.Pool.
// Each method returns pgx's concrete types, which satisfy our subset interfaces.
type pgxDB struct{ pool *pgxpool.Pool }

func (d pgxDB) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return d.pool.Query(ctx, sql, args...)
}

func (d pgxDB) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return d.pool.QueryRow(ctx, sql, args...)
}

func (d pgxDB) Exec(ctx context.Context, sql string, args ...any) (CommandTag, error) {
	return d.pool.Exec(ctx, sql, args...)
}

func (d pgxDB) Begin(ctx context.Context) (Tx, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return pgxTx{tx: tx}, nil
}

// pgxTx adapts pgx.Tx to the Tx seam.
type pgxTx struct{ tx pgx.Tx }

func (t pgxTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return t.tx.Query(ctx, sql, args...)
}

func (t pgxTx) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return t.tx.QueryRow(ctx, sql, args...)
}

func (t pgxTx) Exec(ctx context.Context, sql string, args ...any) (CommandTag, error) {
	return t.tx.Exec(ctx, sql, args...)
}

func (t pgxTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t pgxTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

// Compile-time check that pgconn.CommandTag satisfies CommandTag.
var _ CommandTag = pgconn.CommandTag{}
