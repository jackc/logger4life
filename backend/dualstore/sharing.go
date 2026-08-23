package dualstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) CreateShareToken(ctx context.Context, userID, logID string, token []byte) error {
	return compareError("CreateShareToken",
		func() error { return s.primary.CreateShareToken(ctx, userID, logID, token) },
		func() error { return s.secondary.CreateShareToken(ctx, userID, logID, token) })
}

func (s *Store) DeleteShareToken(ctx context.Context, userID, logID string) error {
	return compareError("DeleteShareToken",
		func() error { return s.primary.DeleteShareToken(ctx, userID, logID) },
		func() error { return s.secondary.DeleteShareToken(ctx, userID, logID) })
}

func (s *Store) ListSharedUsers(ctx context.Context, userID, logID string) ([]core.SharedUser, error) {
	return compareCall("ListSharedUsers",
		func() ([]core.SharedUser, error) { return s.primary.ListSharedUsers(ctx, userID, logID) },
		func() ([]core.SharedUser, error) { return s.secondary.ListSharedUsers(ctx, userID, logID) })
}

func (s *Store) RemoveSharedUser(ctx context.Context, userID, logID, shareID string) error {
	return compareError("RemoveSharedUser",
		func() error { return s.primary.RemoveSharedUser(ctx, userID, logID, shareID) },
		func() error { return s.secondary.RemoveSharedUser(ctx, userID, logID, shareID) })
}

func (s *Store) GetShareInfo(ctx context.Context, userID string, token []byte) (core.ShareInfo, error) {
	return compareCall("GetShareInfo",
		func() (core.ShareInfo, error) { return s.primary.GetShareInfo(ctx, userID, token) },
		func() (core.ShareInfo, error) { return s.secondary.GetShareInfo(ctx, userID, token) })
}

func (s *Store) JoinSharedLog(ctx context.Context, shareID, userID string, token []byte) (core.JoinSharedLogResult, error) {
	return compareCall("JoinSharedLog",
		func() (core.JoinSharedLogResult, error) { return s.primary.JoinSharedLog(ctx, shareID, userID, token) },
		func() (core.JoinSharedLogResult, error) {
			return s.secondary.JoinSharedLog(ctx, shareID, userID, token)
		})
}
