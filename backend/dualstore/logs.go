package dualstore

import (
	"context"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

func (s *Store) CreateLog(ctx context.Context, id, userID, name string, fields []domain.FieldDefinition) (core.Log, error) {
	return compareCall("CreateLog",
		func() (core.Log, error) { return s.primary.CreateLog(ctx, id, userID, name, fields) },
		func() (core.Log, error) { return s.secondary.CreateLog(ctx, id, userID, name, fields) })
}

func (s *Store) GetLog(ctx context.Context, userID, logID string) (core.Log, error) {
	return compareCall("GetLog",
		func() (core.Log, error) { return s.primary.GetLog(ctx, userID, logID) },
		func() (core.Log, error) { return s.secondary.GetLog(ctx, userID, logID) })
}

func (s *Store) UpdateLog(ctx context.Context, userID, logID, name string, fields []domain.FieldDefinition) (core.Log, error) {
	return compareCall("UpdateLog",
		func() (core.Log, error) { return s.primary.UpdateLog(ctx, userID, logID, name, fields) },
		func() (core.Log, error) { return s.secondary.UpdateLog(ctx, userID, logID, name, fields) })
}

func (s *Store) DeleteLog(ctx context.Context, userID, logID string) error {
	return compareError("DeleteLog",
		func() error { return s.primary.DeleteLog(ctx, userID, logID) },
		func() error { return s.secondary.DeleteLog(ctx, userID, logID) })
}

func (s *Store) ListLogs(ctx context.Context, userID string) ([]core.Log, error) {
	return compareCall("ListLogs",
		func() ([]core.Log, error) { return s.primary.ListLogs(ctx, userID) },
		func() ([]core.Log, error) { return s.secondary.ListLogs(ctx, userID) })
}

func (s *Store) LogFieldDefinitions(ctx context.Context, userID, logID string) ([]domain.FieldDefinition, error) {
	return compareCall("LogFieldDefinitions",
		func() ([]domain.FieldDefinition, error) { return s.primary.LogFieldDefinitions(ctx, userID, logID) },
		func() ([]domain.FieldDefinition, error) { return s.secondary.LogFieldDefinitions(ctx, userID, logID) })
}

func (s *Store) CreateLogEntry(ctx context.Context, id, userID, logID string, fields map[string]any, occurredAt time.Time) (domain.LogEntry, error) {
	return compareCall("CreateLogEntry",
		func() (domain.LogEntry, error) {
			return s.primary.CreateLogEntry(ctx, id, userID, logID, fields, occurredAt)
		},
		func() (domain.LogEntry, error) {
			return s.secondary.CreateLogEntry(ctx, id, userID, logID, fields, occurredAt)
		})
}

func (s *Store) ListLogEntries(ctx context.Context, userID, logID string) ([]domain.LogEntry, error) {
	return compareCall("ListLogEntries",
		func() ([]domain.LogEntry, error) { return s.primary.ListLogEntries(ctx, userID, logID) },
		func() ([]domain.LogEntry, error) { return s.secondary.ListLogEntries(ctx, userID, logID) })
}

func (s *Store) UpdateLogEntry(ctx context.Context, userID, logID, entryID string, fields map[string]any, occurredAt time.Time) (domain.LogEntry, error) {
	return compareCall("UpdateLogEntry",
		func() (domain.LogEntry, error) {
			return s.primary.UpdateLogEntry(ctx, userID, logID, entryID, fields, occurredAt)
		},
		func() (domain.LogEntry, error) {
			return s.secondary.UpdateLogEntry(ctx, userID, logID, entryID, fields, occurredAt)
		})
}

func (s *Store) DeleteLogEntry(ctx context.Context, userID, logID, entryID string) error {
	return compareError("DeleteLogEntry",
		func() error { return s.primary.DeleteLogEntry(ctx, userID, logID, entryID) },
		func() error { return s.secondary.DeleteLogEntry(ctx, userID, logID, entryID) })
}
