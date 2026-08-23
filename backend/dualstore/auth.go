package dualstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) CreateUser(ctx context.Context, id, username string, email *string, passwordHash string) (core.User, error) {
	return compareCall("CreateUser",
		func() (core.User, error) { return s.primary.CreateUser(ctx, id, username, email, passwordHash) },
		func() (core.User, error) { return s.secondary.CreateUser(ctx, id, username, email, passwordHash) })
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (core.User, string, error) {
	primaryUser, primaryHash, primaryErr := s.primary.GetUserByUsername(ctx, username)
	secondaryUser, secondaryHash, secondaryErr := s.secondary.GetUserByUsername(ctx, username)
	check("GetUserByUsername", struct {
		User core.User
		Hash string
	}{primaryUser, primaryHash}, struct {
		User core.User
		Hash string
	}{secondaryUser, secondaryHash}, primaryErr, secondaryErr)
	return primaryUser, primaryHash, primaryErr
}

func (s *Store) GetUserByID(ctx context.Context, userID string) (core.User, error) {
	return compareCall("GetUserByID",
		func() (core.User, error) { return s.primary.GetUserByID(ctx, userID) },
		func() (core.User, error) { return s.secondary.GetUserByID(ctx, userID) })
}

func (s *Store) UpdateUserEmail(ctx context.Context, userID string, email *string) (core.User, error) {
	return compareCall("UpdateUserEmail",
		func() (core.User, error) { return s.primary.UpdateUserEmail(ctx, userID, email) },
		func() (core.User, error) { return s.secondary.UpdateUserEmail(ctx, userID, email) })
}

func (s *Store) GetUserPasswordHash(ctx context.Context, userID string) (string, error) {
	return compareCall("GetUserPasswordHash",
		func() (string, error) { return s.primary.GetUserPasswordHash(ctx, userID) },
		func() (string, error) { return s.secondary.GetUserPasswordHash(ctx, userID) })
}

func (s *Store) UpdateUserPasswordHash(ctx context.Context, userID, passwordHash string) error {
	return compareError("UpdateUserPasswordHash",
		func() error { return s.primary.UpdateUserPasswordHash(ctx, userID, passwordHash) },
		func() error { return s.secondary.UpdateUserPasswordHash(ctx, userID, passwordHash) })
}

func (s *Store) CreateSession(ctx context.Context, session core.Session) error {
	return compareError("CreateSession",
		func() error { return s.primary.CreateSession(ctx, session) },
		func() error { return s.secondary.CreateSession(ctx, session) })
}

func (s *Store) GetUserBySessionToken(ctx context.Context, token []byte) (core.User, error) {
	return compareCall("GetUserBySessionToken",
		func() (core.User, error) { return s.primary.GetUserBySessionToken(ctx, token) },
		func() (core.User, error) { return s.secondary.GetUserBySessionToken(ctx, token) })
}

func (s *Store) DeleteSessionByToken(ctx context.Context, token []byte) error {
	return compareError("DeleteSessionByToken",
		func() error { return s.primary.DeleteSessionByToken(ctx, token) },
		func() error { return s.secondary.DeleteSessionByToken(ctx, token) })
}
