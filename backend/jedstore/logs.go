package jedstore

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

// visibleToUser is the rule for reaching one log: the caller owns it or is a
// member of it. A placement row alone is not enough. Placements record how a
// user has organized a log and deliberately outlive a removed membership, so
// that rejoining restores the folder it was filed in — which meant that
// treating a placement as permission left a removed member able to read and
// write the log by ID long after it vanished from their list.
//
// It is written against $1 as the user and the logs table aliased to l.
const visibleToUser = `(l.user_id = $1 OR EXISTS (SELECT 1 FROM log_shares ls WHERE ls.log_id = l.id AND ls.user_id = $1))`

// definedFields keeps the empty case distinguishable from a missing one in
// both directions: the fields column is NOT NULL, and a client renders an
// empty list differently from a null.
func definedFields(fields []domain.FieldDefinition) []domain.FieldDefinition {
	if fields == nil {
		return []domain.FieldDefinition{}
	}
	return fields
}

func (s *Store) CreateLog(ctx context.Context, id, userID, name string, fields []domain.FieldDefinition) (core.Log, error) {
	fields = definedFields(fields)
	var l core.Log
	e := s.InTx(ctx, func(ctx context.Context) error {
		tx := s.conn(ctx)
		e := tx.QueryRow(ctx, `INSERT INTO all_logs(id,user_id,name,fields) VALUES($1,$2,$3,$4) RETURNING id,name,fields,created_at,updated_at`, id, userID, name, fields).Scan(&l.ID, &l.Name, &l.Fields, &l.CreatedAt, &l.UpdatedAt)
		if e != nil {
			if uniqueViolation(e, "logs_user_id_name_unq") {
				return core.ErrLogNameTaken
			}
			return e
		}
		var position, homePosition int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(position) FILTER(WHERE folder_id IS NULL)+1,0),COALESCE(max(home_position) FILTER(WHERE pinned_to_home)+1,0) FROM user_log_placements WHERE user_id=$1`, userID).Scan(&position, &homePosition); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO user_log_placements(user_id,log_id,folder_id,position,pinned_to_home,home_position) VALUES($1,$2,NULL,$3,true,$4) RETURNING position,pinned_to_home,home_position`, userID, l.ID, position, homePosition).Scan(&l.Position, &l.PinnedToHome, &l.HomePosition)
	})
	if e != nil {
		return core.Log{}, e
	}
	l.IsOwner = true
	l.Fields = definedFields(l.Fields)
	return l, nil
}

func (s *Store) GetLog(ctx context.Context, userID, logID string) (core.Log, error) {
	var l core.Log
	var token []byte
	e := s.conn(ctx).QueryRow(ctx, `SELECT l.id,l.name,l.fields,l.user_id=$1,l.share_token,p.folder_id,p.position,p.pinned_to_home,p.home_position,l.created_at,l.updated_at FROM all_logs l JOIN user_log_placements p ON p.log_id=l.id AND p.user_id=$1 WHERE l.id=$2 AND `+visibleToUser, userID, logID).Scan(&l.ID, &l.Name, &l.Fields, &l.IsOwner, &token, &l.FolderID, &l.Position, &l.PinnedToHome, &l.HomePosition, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(e, errNoRows) {
		return core.Log{}, core.ErrLogNotFound
	}
	if e != nil {
		return core.Log{}, e
	}
	if l.IsOwner && token != nil {
		v := hex.EncodeToString(token)
		l.ShareToken = &v
	}
	l.Fields = definedFields(l.Fields)
	return l, nil
}
func (s *Store) UpdateLog(ctx context.Context, userID, logID, name string, fields []domain.FieldDefinition) (core.Log, error) {
	fields = definedFields(fields)
	var l core.Log
	var token []byte
	e := s.InTx(ctx, func(ctx context.Context) error {
		conn := s.conn(ctx)
		if err := conn.QueryRow(ctx, `UPDATE all_logs SET name=$1,fields=$2,updated_at=now() WHERE id=$3 AND user_id=$4 RETURNING id,name,fields,share_token,created_at,updated_at`, name, fields, logID, userID).Scan(&l.ID, &l.Name, &l.Fields, &token, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return err
		}
		return conn.QueryRow(ctx, `SELECT folder_id,position,pinned_to_home,home_position FROM user_log_placements WHERE log_id=$1 AND user_id=$2`, l.ID, userID).Scan(&l.FolderID, &l.Position, &l.PinnedToHome, &l.HomePosition)
	})
	if errors.Is(e, errNoRows) {
		return core.Log{}, core.ErrLogNotFound
	}
	if e != nil {
		if uniqueViolation(e, "logs_user_id_name_unq") {
			return core.Log{}, core.ErrLogNameTaken
		}
		return core.Log{}, e
	}
	l.IsOwner = true
	if token != nil {
		v := hex.EncodeToString(token)
		l.ShareToken = &v
	}
	l.Fields = definedFields(l.Fields)
	return l, nil
}
func (s *Store) DeleteLog(ctx context.Context, userID, logID string) error {
	return s.InTx(ctx, func(ctx context.Context) error {
		tx := s.conn(ctx)
		var owned bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM all_logs WHERE id=$1 AND user_id=$2)`, logID, userID).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return core.ErrLogNotFound
		}
		for _, statement := range []string{
			`DELETE FROM all_log_entries WHERE log_id=$1`,
			`DELETE FROM log_shares WHERE log_id=$1`,
			`DELETE FROM user_log_placements WHERE log_id=$1`,
		} {
			if _, err := tx.Exec(ctx, statement, logID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM all_logs WHERE id=$1`, logID)
		return err
	})
}

func (s *Store) ListLogs(ctx context.Context, userID string) ([]core.Log, error) {
	rows, err := s.conn(ctx).Query(ctx, `
		SELECT l.id, l.name, l.fields, l.user_id = $1,
		       p.folder_id, p.position, p.pinned_to_home, p.home_position,
		       l.created_at, l.updated_at
		FROM all_logs l
		JOIN user_log_placements p ON p.log_id = l.id AND p.user_id = $1
		WHERE `+visibleToUser+`
		ORDER BY p.folder_id NULLS FIRST, p.position`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := []core.Log{}
	for rows.Next() {
		var l core.Log
		if err := rows.Scan(&l.ID, &l.Name, &l.Fields, &l.IsOwner, &l.FolderID, &l.Position, &l.PinnedToHome, &l.HomePosition, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}
