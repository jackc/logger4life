package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
)

// stubFolderStore is enough of a FolderStore for an action to succeed, so the
// audit trail can be observed for a call that actually did something.
type stubFolderStore struct{}

func (stubFolderStore) CreateFolder(context.Context, string, string, *string) (core.Folder, error) {
	return core.Folder{ID: "folder-1", Name: "Health"}, nil
}
func (stubFolderStore) ListFolders(context.Context, string) ([]core.Folder, error) {
	return []core.Folder{}, nil
}
func (stubFolderStore) RenameFolder(context.Context, string, string, string) (core.Folder, error) {
	return core.Folder{}, nil
}
func (stubFolderStore) MoveFolder(context.Context, string, string, *string, int) error { return nil }
func (stubFolderStore) DeleteFolder(context.Context, string, string) error             { return nil }

// auditRecorder builds a core wired exactly as the composition root wires it,
// over a logger whose output the test can read back.
func auditRecorder(t *testing.T) (*core.Core, func() []map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	app := core.New(core.Config{
		Folders:    stubFolderStore{},
		Middleware: []core.Middleware{core.RequireUser(), auditMiddleware(logger)},
	})

	return app, func() []map[string]any {
		var records []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatalf("audit line %q is not JSON: %v", line, err)
			}
			records = append(records, record)
		}
		return records
	}
}

// A change to state is recorded with who made it, so the trail answers the
// question it exists for.
func TestAuditRecordsWhoChangedWhat(t *testing.T) {
	app, records := auditRecorder(t)
	ctx := core.WithUserID(context.Background(), "user-1")

	if _, err := core.CreateFolder.Call(ctx, app, core.CreateFolderParams{Name: "Health"}); err != nil {
		t.Fatal(err)
	}

	got := records()
	if len(got) != 1 {
		t.Fatalf("recorded %d lines, want 1: %#v", len(got), got)
	}
	if got[0]["msg"] != "action performed" {
		t.Errorf("msg = %v, want the success to be marked as one", got[0]["msg"])
	}
	if got[0]["action"] != "create_folder" {
		t.Errorf("action = %v, want create_folder", got[0]["action"])
	}
	if got[0]["user_id"] != "user-1" {
		t.Errorf("user_id = %v, want the caller", got[0]["user_id"])
	}
}

// Reads are the bulk of traffic and the request log already covers them;
// recording them here would bury the writes.
func TestAuditIgnoresReads(t *testing.T) {
	app, records := auditRecorder(t)
	ctx := core.WithUserID(context.Background(), "user-1")

	if _, err := core.ListFolders.Call(ctx, app, core.ListFoldersParams{}); err != nil {
		t.Fatal(err)
	}

	if got := records(); len(got) != 0 {
		t.Errorf("a read was recorded: %#v", got)
	}
}

// A refusal is worth recording too — more so when it repeats — and it must be
// distinguishable from a success.
func TestAuditDistinguishesARefusalFromASuccess(t *testing.T) {
	app, records := auditRecorder(t)
	ctx := core.WithUserID(context.Background(), "user-1")

	_, _ = core.CreateFolder.Call(ctx, app, core.CreateFolderParams{Name: ""})

	got := records()
	if len(got) != 1 {
		t.Fatalf("recorded %d lines, want 1: %#v", len(got), got)
	}
	if got[0]["msg"] != "action refused" {
		t.Errorf("msg = %v, want the refusal to be marked as one", got[0]["msg"])
	}
	if got[0]["error"] == nil || got[0]["error"] == "" {
		t.Error("a refusal was recorded without saying why")
	}
}

// An anonymous caller is turned away by RequireUser, which sits outside the
// audit middleware, so the trail does not fill up with attempts that never
// reached an action.
func TestAuditDoesNotRecordCallsRequireUserTurnedAway(t *testing.T) {
	app, records := auditRecorder(t)

	if _, err := core.CreateFolder.Call(context.Background(), app, core.CreateFolderParams{Name: "Health"}); err == nil {
		t.Fatal("an anonymous caller was not refused")
	}
	if got := records(); len(got) != 0 {
		t.Errorf("a refused anonymous call was recorded: %#v", got)
	}
}
