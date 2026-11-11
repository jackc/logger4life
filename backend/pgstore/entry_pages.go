package pgstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

func (s *Store) ListLogEntriesPage(ctx context.Context, user string, p core.EntryPageQuery) ([]domain.LogEntry, error) {
	if _, err := s.LogFieldDefinitions(ctx, user, p.LogID); err != nil {
		return nil, err
	}
	args := []any{p.LogID}
	clauses := []string{"le.log_id=$1"}
	add := func(clause string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if p.StartAt != nil {
		add("le.occurred_at >= $%d", *p.StartAt)
	}
	if p.EndAt != nil {
		add("le.occurred_at < $%d", *p.EndAt)
	}
	direction, comparison := "DESC", "<"
	if p.Order == "asc" {
		direction, comparison = "ASC", ">"
	}
	if p.AfterAt != nil {
		args = append(args, *p.AfterAt, p.AfterID)
		clauses = append(clauses, fmt.Sprintf("(le.occurred_at %s $%d OR (le.occurred_at = $%d AND le.id %s $%d))", comparison, len(args)-1, len(args)-1, comparison, len(args)))
	}
	args = append(args, min(max(p.Limit, 1), 101))
	query := "SELECT le.id,le.log_id,le.user_id,u.username,le.fields,le.occurred_at,le.created_at,le.updated_at,le.note FROM log_entries le JOIN users u ON u.id=le.user_id WHERE " + strings.Join(clauses, " AND ") + fmt.Sprintf(" ORDER BY le.occurred_at %s, le.id %s LIMIT $%d", direction, direction, len(args))
	rows, err := s.conn(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.LogEntry{}
	for rows.Next() {
		entry, err := scanLogEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}
