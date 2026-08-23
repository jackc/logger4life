// Package jedstore implements Logger4Life's persistence ports against one
// embedded jed database.
package jedstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/gofrs/uuid/v5"
	jed "github.com/jackc/jed/impl/go"
	migrate "github.com/jackc/jed/migrate/go"
	jedmigrations "github.com/jackc/logger4life/db/migrations/jed"
	pgsqlarbiter "github.com/jackc/pgsqlarbiter-go"
)

type Store struct {
	db             *jed.Database
	userSQLArbiter *pgsqlarbiter.Arbiter
}

// Open opens dataDir/logger4life.jed, creating it when necessary, and applies
// every embedded migration before returning it.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("jedstore: create data directory: %w", err)
	}
	migrations, err := migrate.LoadMigrationsFS(jedmigrations.FS, jedmigrations.Root)
	if err != nil {
		return nil, fmt.Errorf("jedstore: load migrations: %w", err)
	}
	path := filepath.Join(dataDir, "logger4life.jed")
	var db *jed.Database
	statErr := error(nil)
	if _, statErr = os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
		db, err = jed.CreateDatabase(jed.CreateOptions{Path: path})
	} else if statErr == nil {
		db, err = jed.OpenDatabase(path)
	} else {
		err = statErr
	}
	if err != nil {
		return nil, fmt.Errorf("jedstore: open database: %w", err)
	}
	migrator, err := migrate.NewMigrator(db, migrations, migrate.Options{})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("jedstore: initialize migrations: %w", err)
	}
	defer migrator.Close()
	if err := migrator.Migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("jedstore: migrate database: %w", err)
	}
	return &Store{db: db, userSQLArbiter: newUserSQLArbiter()}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error {
	var one int64
	return s.db.QueryRow(ctx, `SELECT 1`).Scan(&one)
}

func newUserID() (string, error) {
	id, err := uuid.NewV4()
	return id.String(), err
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	return id.String(), err
}
