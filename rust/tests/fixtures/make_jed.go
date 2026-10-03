//go:build ignore

// Build the committed cross-language fixture with the existing Go engine.
// From the repository root: go run rust/tests/fixtures/make_jed.go PATH
// Add --verify to inspect a file after the Rust compatibility test has written it.
package main

import (
	"context"
	"fmt"
	jed "github.com/jackc/jed/impl/go"
	migrate "github.com/jackc/jed/migrate/go"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: make_jed PATH [--verify]")
	}
	ctx := context.Background()
	if len(os.Args) > 2 && os.Args[2] == "--verify" {
		db, err := jed.OpenDatabase(os.Args[1])
		if err != nil {
			return err
		}
		defer db.Close()
		var note string
		if err := db.QueryRow(ctx, "SELECT note FROM all_log_entries WHERE id=$1", "00000000-0000-7000-8000-000000000003").Scan(&note); err != nil {
			return err
		}
		if note != "Updated by Rust" {
			return fmt.Errorf("Rust update missing: %q", note)
		}
		return nil
	}
	db, err := jed.CreateDatabase(jed.CreateOptions{Path: os.Args[1]})
	if err != nil {
		return err
	}
	defer db.Close()
	migrations, err := migrate.LoadMigrations("db/migrations/jed")
	if err != nil {
		return err
	}
	migrator, err := migrate.NewMigrator(db, migrations, migrate.Options{})
	if err != nil {
		return err
	}
	defer migrator.Close()
	if err = migrator.Migrate(); err != nil {
		return err
	}
	user := "00000000-0000-4000-8000-000000000001"
	log := "00000000-0000-7000-8000-000000000002"
	entry := "00000000-0000-7000-8000-000000000003"
	if _, err = db.Exec(ctx, "INSERT INTO users(id,username,password_hash,email) VALUES($1,$2,$3,$4)", user, "go_fixture", "$2a$10$7EqJtq98hPqEX7fNZaFWoO5D7EfHV2RRZm56WXTyJTAHA.F6WhvJS", "go@example.test"); err != nil {
		return err
	}
	if _, err = db.Exec(ctx, "INSERT INTO all_logs(id,user_id,name,fields,share_token) VALUES($1,$2,$3,$4,$5)", log, user, "Go log", `[{"name":"dose","type":"number","required":true}]`, []byte{0, 1, 2, 127, 128, 255}); err != nil {
		return err
	}
	when := time.Date(2026, 10, 3, 12, 34, 56, 123456000, time.UTC)
	if _, err = db.Exec(ctx, "INSERT INTO all_log_entries(id,log_id,user_id,fields,occurred_at,note) VALUES($1,$2,$3,$4,$5,$6)", entry, log, user, `{"dose":42}`, when, "Written by Go"); err != nil {
		return err
	}
	if _, err = db.Exec(ctx, "INSERT INTO oauth_clients(id,redirect_uris,client_name) VALUES($1,$2,$3)", "fixture-client", `["https://example.test/callback"]`, "Go client"); err != nil {
		return err
	}
	return nil
}
