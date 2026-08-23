package jedstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	jed "github.com/jackc/jed/impl/go"
	"github.com/jackc/logger4life/backend/domain"
)

type txKey struct{}

var errNoRows = jed.ErrNoRows

func uniqueViolation(err error, constraint string) bool {
	var engineErr *jed.EngineError
	return errors.As(err, &engineErr) && engineErr.Code() == "23505" &&
		strings.Contains(engineErr.ConstraintName, constraint)
}

// db normalizes jed's pgx-shaped API to the small surface used by the store.
// It also owns the two intentional representation differences: identifiers
// are text in the embedded schema, while structured Go values bind and scan
// through JSON.
type db interface {
	Exec(context.Context, string, ...any) (commandTag, error)
	Query(context.Context, string, ...any) (*rows, error)
	QueryRow(context.Context, string, ...any) *row
}

type queryer struct{ q jed.Queryer }

type commandTag struct{ rowsAffected int64 }

func (t commandTag) RowsAffected() int64 { return t.rowsAffected }

func (q queryer) Exec(ctx context.Context, sql string, args ...any) (commandTag, error) {
	args, err := adaptArgs(args)
	if err != nil {
		return commandTag{}, err
	}
	result, err := q.q.Exec(ctx, sql, args...)
	if err != nil {
		return commandTag{}, err
	}
	n, _ := result.RowsAffected()
	return commandTag{rowsAffected: n}, nil
}

func (q queryer) Query(ctx context.Context, sql string, args ...any) (*rows, error) {
	args, err := adaptArgs(args)
	if err != nil {
		return nil, err
	}
	r, err := q.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &rows{Rows: r}, nil
}

func (q queryer) QueryRow(ctx context.Context, sql string, args ...any) *row {
	args, err := adaptArgs(args)
	if err != nil {
		return &row{err: err}
	}
	return &row{Row: q.q.QueryRow(ctx, sql, args...)}
}

func (s *Store) conn(ctx context.Context) db {
	if tx, ok := ctx.Value(txKey{}).(*jed.Transaction); ok {
		return queryer{q: tx}
	}
	return queryer{q: s.db}
}

func (s *Store) InTx(ctx context.Context, fn func(context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(*jed.Transaction); ok {
		return fn(ctx)
	}
	return s.db.Update(func(tx *jed.Transaction) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

type rowScanner interface{ Scan(...any) error }

type queryRower interface {
	QueryRow(context.Context, string, ...any) *row
}

type row struct {
	Row *jed.Row
	err error
}

func (r *row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return r.Row.Scan(adaptDestinations(dest)...)
}

type rows struct{ *jed.Rows }

func (r *rows) Scan(dest ...any) error {
	return r.Rows.Scan(adaptDestinations(dest)...)
}

func adaptArgs(args []any) ([]any, error) {
	adapted := make([]any, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case *string:
			if v != nil {
				adapted[i] = *v
			}
		case []domain.FieldDefinition, map[string]any, []string, json.RawMessage:
			encoded, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("jedstore: encode parameter %d: %w", i+1, err)
			}
			adapted[i] = string(encoded)
		default:
			adapted[i] = arg
		}
	}
	return adapted, nil
}

func adaptDestinations(dest []any) []any {
	adapted := make([]any, len(dest))
	for i, d := range dest {
		switch v := d.(type) {
		case **string:
			adapted[i] = &nullableStringScanner{dest: v}
		case *[]byte:
			adapted[i] = &nullableBytesScanner{dest: v}
		case *[]domain.FieldDefinition, *map[string]any, *[]string, *json.RawMessage:
			adapted[i] = &jsonScanner{dest: d}
		default:
			adapted[i] = d
		}
	}
	return adapted
}

type nullableBytesScanner struct{ dest *[]byte }

func (s *nullableBytesScanner) ScanJed(v jed.Value) error {
	if v.Kind == jed.ValNull {
		*s.dest = nil
		return nil
	}
	if v.Kind != jed.ValBytea {
		return fmt.Errorf("cannot scan %s into bytes", v.Render())
	}
	value, err := hex.DecodeString(strings.TrimPrefix(v.Render(), `\x`))
	if err != nil {
		return fmt.Errorf("decode bytea: %w", err)
	}
	*s.dest = value
	return nil
}

type nullableStringScanner struct{ dest **string }

func (s *nullableStringScanner) ScanJed(v jed.Value) error {
	if v.Kind == jed.ValNull {
		*s.dest = nil
		return nil
	}
	if v.Kind != jed.ValText {
		return fmt.Errorf("cannot scan %s into nullable string", v.Render())
	}
	value := v.Render()
	*s.dest = &value
	return nil
}

type jsonScanner struct{ dest any }

func (s *jsonScanner) ScanJed(v jed.Value) error {
	if v.Kind == jed.ValNull {
		return nil
	}
	return json.Unmarshal([]byte(v.Render()), s.dest)
}
