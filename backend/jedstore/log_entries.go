package jedstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

func (s *Store) LogFieldDefinitions(ctx context.Context, userID, logID string) ([]domain.FieldDefinition, error) {
	var defs []domain.FieldDefinition
	e := s.conn(ctx).QueryRow(ctx, `SELECT l.fields FROM all_logs l JOIN user_log_placements p ON p.log_id=l.id AND p.user_id=$1 WHERE l.id=$2 AND `+visibleToUser, userID, logID).Scan(&defs)
	if errors.Is(e, errNoRows) {
		return nil, core.ErrLogNotFound
	}
	return defs, e
}
func scanLogEntry(row rowScanner) (domain.LogEntry, error) {
	var e domain.LogEntry
	err := row.Scan(&e.ID, &e.LogID, &e.UserID, &e.Username, &e.Fields, &e.OccurredAt, &e.CreatedAt, &e.UpdatedAt)
	if e.Fields == nil {
		e.Fields = map[string]any{}
	}
	return e, err
}

func scanStoredLogEntry(row rowScanner) (domain.LogEntry, error) {
	var e domain.LogEntry
	err := row.Scan(&e.ID, &e.LogID, &e.UserID, &e.Fields, &e.OccurredAt, &e.CreatedAt, &e.UpdatedAt)
	if e.Fields == nil {
		e.Fields = map[string]any{}
	}
	return e, err
}

func (s *Store) CreateLogEntry(ctx context.Context, id, userID, logID string, fields map[string]any, occurredAt time.Time) (domain.LogEntry, error) {
	var entry domain.LogEntry
	err := s.InTx(ctx, func(ctx context.Context) error {
		var err error
		entry, err = scanStoredLogEntry(s.conn(ctx).QueryRow(ctx, `INSERT INTO all_log_entries(id,log_id,user_id,fields,occurred_at) VALUES($1,$2,$3,$4,$5) RETURNING id,log_id,user_id,fields,occurred_at,created_at,updated_at`, id, logID, userID, fields, occurredAt))
		if err != nil {
			return err
		}
		return s.conn(ctx).QueryRow(ctx, `SELECT username FROM users WHERE id=$1`, entry.UserID).Scan(&entry.Username)
	})
	return entry, err
}
func (s *Store) ListLogEntries(ctx context.Context, userID, logID string) ([]domain.LogEntry, error) {
	if _, e := s.LogFieldDefinitions(ctx, userID, logID); e != nil {
		return nil, e
	}
	rows, e := s.conn(ctx).Query(ctx, `SELECT le.id,le.log_id,le.user_id,u.username,le.fields,le.occurred_at,le.created_at,le.updated_at FROM all_log_entries le JOIN users u ON u.id=le.user_id WHERE le.log_id=$1 ORDER BY le.occurred_at DESC`, logID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.LogEntry{}
	for rows.Next() {
		v, e := scanLogEntry(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UpdateLogEntry(ctx context.Context, userID, logID, entryID string, fields map[string]any, occurred time.Time) (domain.LogEntry, error) {
	var v domain.LogEntry
	e := s.InTx(ctx, func(ctx context.Context) error {
		var err error
		v, err = scanStoredLogEntry(s.conn(ctx).QueryRow(ctx, `UPDATE all_log_entries SET fields=$1,occurred_at=$2,updated_at=now() WHERE id=$3 AND log_id=$4 RETURNING id,log_id,user_id,fields,occurred_at,created_at,updated_at`, fields, occurred, entryID, logID))
		if err != nil {
			return err
		}
		return s.conn(ctx).QueryRow(ctx, `SELECT username FROM users WHERE id=$1`, v.UserID).Scan(&v.Username)
	})
	if errors.Is(e, errNoRows) {
		e = core.ErrLogEntryNotFound
	}
	return v, e
}
func (s *Store) DeleteLogEntry(ctx context.Context, userID, logID, entryID string) error {
	if _, e := s.LogFieldDefinitions(ctx, userID, logID); e != nil {
		return e
	}
	tag, e := s.conn(ctx).Exec(ctx, `DELETE FROM all_log_entries WHERE id=$1 AND log_id=$2`, entryID, logID)
	if e == nil && tag.RowsAffected() == 0 {
		return core.ErrLogEntryNotFound
	}
	return e
}
