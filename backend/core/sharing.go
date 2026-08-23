package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrShareNotFound    = errors.New("share not found")
	ErrInvalidShareLink = errors.New("invalid share link")
	ErrAlreadyOwnLog    = errors.New("you already own this log")
)

type SharedUser struct {
	ID       string    `json:"id"`
	Username string    `json:"username"`
	SharedAt time.Time `json:"shared_at"`
}

type ShareInfo struct {
	LogID         string `json:"log_id"`
	LogName       string `json:"log_name"`
	OwnerUsername string `json:"owner_username"`
	IsOwner       bool   `json:"is_owner"`
	AlreadyMember bool   `json:"already_member"`
}

type JoinSharedLogResult struct {
	LogID         string `json:"log_id"`
	LogName       string `json:"log_name"`
	AlreadyMember bool   `json:"-"`
}

// SharingStore is the driven persistence port for share links and log
// memberships. Implementations must enforce ownership and user scope.
type SharingStore interface {
	CreateShareToken(context.Context, string, string, []byte) error
	DeleteShareToken(context.Context, string, string) error
	ListSharedUsers(context.Context, string, string) ([]SharedUser, error)
	RemoveSharedUser(context.Context, string, string, string) error
	GetShareInfo(context.Context, string, []byte) (ShareInfo, error)
	JoinSharedLog(context.Context, string, string, []byte) (JoinSharedLogResult, error)
}

type CreateShareTokenParams struct {
	LogID string `json:"log_id"`
}

func (p *CreateShareTokenParams) Validate() error { return validID("log_id", p.LogID) }

type ShareToken struct {
	ShareToken string `json:"share_token"`
}

var CreateShareToken = Define(ActionDef[CreateShareTokenParams, ShareToken]{Name: "create_share_token", Description: "Create a share link for an owned log.", Mutating: true, Handler: func(ctx context.Context, c *Core, p CreateShareTokenParams) (ShareToken, error) {
	userID, err := requiredUser(ctx)
	if err != nil {
		return ShareToken{}, err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return ShareToken{}, err
	}
	if err := c.sharing.CreateShareToken(ctx, userID, p.LogID, token); err != nil {
		return ShareToken{}, err
	}
	return ShareToken{ShareToken: hex.EncodeToString(token)}, nil
}})

type DeleteShareTokenParams struct {
	LogID string `json:"log_id"`
}

func (p *DeleteShareTokenParams) Validate() error { return validID("log_id", p.LogID) }

var DeleteShareToken = Define(ActionDef[DeleteShareTokenParams, struct{}]{Name: "delete_share_token", Description: "Revoke the share link for an owned log.", Mutating: true, Handler: func(ctx context.Context, c *Core, p DeleteShareTokenParams) (struct{}, error) {
	userID, err := requiredUser(ctx)
	if err == nil {
		err = c.sharing.DeleteShareToken(ctx, userID, p.LogID)
	}
	return struct{}{}, err
}})

type ListSharedUsersParams struct {
	LogID string `json:"log_id"`
}

func (p *ListSharedUsersParams) Validate() error { return validID("log_id", p.LogID) }

var ListSharedUsers = Define(ActionDef[ListSharedUsersParams, []SharedUser]{Name: "list_shared_users", Description: "List users sharing an owned log.", Handler: func(ctx context.Context, c *Core, p ListSharedUsersParams) ([]SharedUser, error) {
	userID, err := requiredUser(ctx)
	if err != nil {
		return nil, err
	}
	return c.sharing.ListSharedUsers(ctx, userID, p.LogID)
}})

type RemoveSharedUserParams struct {
	LogID   string `json:"log_id"`
	ShareID string `json:"share_id"`
}

func (p *RemoveSharedUserParams) Validate() error {
	if e := validID("log_id", p.LogID); e != nil {
		return e
	}
	return validID("share_id", p.ShareID)
}

var RemoveSharedUser = Define(ActionDef[RemoveSharedUserParams, struct{}]{Name: "remove_shared_user", Description: "Remove a user from an owned shared log.", Mutating: true, Handler: func(ctx context.Context, c *Core, p RemoveSharedUserParams) (struct{}, error) {
	userID, err := requiredUser(ctx)
	if err == nil {
		err = c.sharing.RemoveSharedUser(ctx, userID, p.LogID, p.ShareID)
	}
	return struct{}{}, err
}})

type GetShareInfoParams struct {
	Token string `json:"token"`
}

func decodeShareToken(token string) ([]byte, error) {
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) == 0 {
		return nil, ErrInvalidShareLink
	}
	return decoded, nil
}

var GetShareInfo = Define(ActionDef[GetShareInfoParams, ShareInfo]{Name: "get_share_info", Description: "Read information about a share link.", Handler: func(ctx context.Context, c *Core, p GetShareInfoParams) (ShareInfo, error) {
	userID, err := requiredUser(ctx)
	if err != nil {
		return ShareInfo{}, err
	}
	token, err := decodeShareToken(p.Token)
	if err != nil {
		return ShareInfo{}, err
	}
	return c.sharing.GetShareInfo(ctx, userID, token)
}})

type JoinSharedLogParams struct {
	Token string `json:"token"`
}

var JoinSharedLog = Define(ActionDef[JoinSharedLogParams, JoinSharedLogResult]{Name: "join_shared_log", Description: "Join a log through a share link.", Mutating: true, Handler: func(ctx context.Context, c *Core, p JoinSharedLogParams) (JoinSharedLogResult, error) {
	userID, err := requiredUser(ctx)
	if err != nil {
		return JoinSharedLogResult{}, err
	}
	token, err := decodeShareToken(p.Token)
	if err != nil {
		return JoinSharedLogResult{}, err
	}
	shareID, err := newID()
	if err != nil {
		return JoinSharedLogResult{}, err
	}
	return c.sharing.JoinSharedLog(ctx, shareID, userID, token)
}})
