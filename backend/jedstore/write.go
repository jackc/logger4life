package jedstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) ClaimWrite(ctx context.Context, user, operation, key, hash string) (string, error) {
	_, err := s.conn(ctx).Exec(ctx, `INSERT INTO mcp_write_receipts(user_id,operation,key,request_hash,result) VALUES($1,$2,$3,$4,'') ON CONFLICT(user_id,operation,key) DO NOTHING`, user, operation, key, hash)
	if err != nil {
		return "", err
	}
	var stored, result string
	err = s.conn(ctx).QueryRow(ctx, `SELECT request_hash,result FROM mcp_write_receipts WHERE user_id=$1 AND operation=$2 AND key=$3`, user, operation, key).Scan(&stored, &result)
	if err != nil {
		return "", err
	}
	if stored != hash {
		return "", core.ErrIdempotencyConflict
	}
	return result, nil
}
func (s *Store) SaveWrite(ctx context.Context, user, operation, key, result string) error {
	_, err := s.conn(ctx).Exec(ctx, `UPDATE mcp_write_receipts SET result=$4 WHERE user_id=$1 AND operation=$2 AND key=$3`, user, operation, key, result)
	return err
}
