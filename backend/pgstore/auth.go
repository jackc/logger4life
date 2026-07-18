package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func translateUserWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "users_username_unq":
			return core.ErrUsernameTaken
		case "users_email_unq":
			return core.ErrEmailTaken
		}
	}
	return err
}

func (s *Store) CreateUser(ctx context.Context, username string, email *string, passwordHash string) (core.User, error) {
	var user core.User
	err := s.conn(ctx).QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash)
		 VALUES ($1, $2, $3)
		 RETURNING id, username, email`,
		username, email, passwordHash,
	).Scan(&user.ID, &user.Username, &user.Email)
	return user, translateUserWriteError(err)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (core.User, string, error) {
	var user core.User
	var passwordHash string
	err := s.conn(ctx).QueryRow(ctx,
		`SELECT id, username, email, password_hash
		 FROM users WHERE lower(username) = lower($1)`, username,
	).Scan(&user.ID, &user.Username, &user.Email, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.User{}, "", core.ErrUserNotFound
	}
	return user, passwordHash, err
}

func (s *Store) GetUserByID(ctx context.Context, userID string) (core.User, error) {
	var user core.User
	err := s.conn(ctx).QueryRow(ctx,
		`SELECT id, username, email FROM users WHERE id = $1`, userID,
	).Scan(&user.ID, &user.Username, &user.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.User{}, core.ErrUserNotFound
	}
	return user, err
}

func (s *Store) UpdateUserEmail(ctx context.Context, userID string, email *string) (core.User, error) {
	var user core.User
	err := s.conn(ctx).QueryRow(ctx,
		`UPDATE users SET email = $1, updated_at = now() WHERE id = $2
		 RETURNING id, username, email`, email, userID,
	).Scan(&user.ID, &user.Username, &user.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.User{}, core.ErrUserNotFound
	}
	return user, translateUserWriteError(err)
}

func (s *Store) GetUserPasswordHash(ctx context.Context, userID string) (string, error) {
	var passwordHash string
	err := s.conn(ctx).QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID,
	).Scan(&passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", core.ErrUserNotFound
	}
	return passwordHash, err
}

func (s *Store) UpdateUserPasswordHash(ctx context.Context, userID, passwordHash string) error {
	tag, err := s.conn(ctx).Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`,
		passwordHash, userID,
	)
	if err == nil && tag.RowsAffected() == 0 {
		return core.ErrUserNotFound
	}
	return err
}

func (s *Store) CreateSession(ctx context.Context, session core.Session) error {
	_, err := s.conn(ctx).Exec(ctx,
		`INSERT INTO sessions (user_id, token, expires_at) VALUES ($1, $2, $3)`,
		session.UserID, session.Token, session.ExpiresAt,
	)
	return err
}

func (s *Store) GetUserBySessionToken(ctx context.Context, token []byte) (core.User, error) {
	var user core.User
	err := s.conn(ctx).QueryRow(ctx,
		`SELECT u.id, u.username, u.email
		 FROM sessions s
		 JOIN users u ON s.user_id = u.id
		 WHERE s.token = $1 AND s.expires_at > now()`, token,
	).Scan(&user.ID, &user.Username, &user.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.User{}, core.ErrInvalidSession
	}
	return user, err
}

func (s *Store) DeleteSessionByToken(ctx context.Context, token []byte) error {
	_, err := s.conn(ctx).Exec(ctx, `DELETE FROM sessions WHERE token = $1`, token)
	return err
}
