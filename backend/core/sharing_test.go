package core

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
)

type fakeSharingStore struct {
	createdUserID  string
	createdLogID   string
	createdToken   []byte
	deletedLogID   string
	listedLogID    string
	removedLogID   string
	removedShareID string
	infoToken      []byte
	joinToken      []byte
	shares         []SharedUser
	info           ShareInfo
	join           JoinSharedLogResult
	err            error
}

func (s *fakeSharingStore) CreateShareToken(_ context.Context, userID, logID string, token []byte) error {
	s.createdUserID = userID
	s.createdLogID = logID
	s.createdToken = append([]byte(nil), token...)
	return s.err
}

func (s *fakeSharingStore) DeleteShareToken(_ context.Context, _ string, logID string) error {
	s.deletedLogID = logID
	return s.err
}

func (s *fakeSharingStore) ListSharedUsers(_ context.Context, _ string, logID string) ([]SharedUser, error) {
	s.listedLogID = logID
	return s.shares, s.err
}

func (s *fakeSharingStore) RemoveSharedUser(_ context.Context, _ string, logID, shareID string) error {
	s.removedLogID = logID
	s.removedShareID = shareID
	return s.err
}

func (s *fakeSharingStore) GetShareInfo(_ context.Context, _ string, token []byte) (ShareInfo, error) {
	s.infoToken = append([]byte(nil), token...)
	return s.info, s.err
}

func (s *fakeSharingStore) JoinSharedLog(_ context.Context, _ string, token []byte) (JoinSharedLogResult, error) {
	s.joinToken = append([]byte(nil), token...)
	return s.join, s.err
}

func TestCreateShareTokenAction(t *testing.T) {
	store := &fakeSharingStore{}
	app := New(Config{Sharing: store})
	ctx := WithUserID(context.Background(), "user-1")

	result, err := CreateShareToken.Call(ctx, app, CreateShareTokenParams{LogID: "log-1"})
	if err != nil {
		t.Fatal(err)
	}
	if store.createdUserID != "user-1" || store.createdLogID != "log-1" {
		t.Fatalf("store scope = (%q, %q)", store.createdUserID, store.createdLogID)
	}
	if len(store.createdToken) != 32 {
		t.Fatalf("token length = %d, want 32", len(store.createdToken))
	}
	decoded, err := hex.DecodeString(result.ShareToken)
	if err != nil || string(decoded) != string(store.createdToken) {
		t.Fatalf("result token does not encode the persisted token")
	}
}

func TestSharingActionsCallStore(t *testing.T) {
	store := &fakeSharingStore{
		shares: []SharedUser{{ID: "share-1", Username: "bob"}},
		info:   ShareInfo{LogID: "log-1", LogName: "Shared"},
		join:   JoinSharedLogResult{LogID: "log-1", LogName: "Shared"},
	}
	app := New(Config{Sharing: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := DeleteShareToken.Call(ctx, app, DeleteShareTokenParams{LogID: "log-1"}); err != nil {
		t.Fatal(err)
	}
	shares, err := ListSharedUsers.Call(ctx, app, ListSharedUsersParams{LogID: "log-1"})
	if err != nil || len(shares) != 1 || shares[0].Username != "bob" {
		t.Fatalf("ListSharedUsers() = %#v, %v", shares, err)
	}
	if _, err := RemoveSharedUser.Call(ctx, app, RemoveSharedUserParams{LogID: "log-1", ShareID: "share-1"}); err != nil {
		t.Fatal(err)
	}
	info, err := GetShareInfo.Call(ctx, app, GetShareInfoParams{Token: "0102"})
	if err != nil || info.LogID != "log-1" {
		t.Fatalf("GetShareInfo() = %#v, %v", info, err)
	}
	joined, err := JoinSharedLog.Call(ctx, app, JoinSharedLogParams{Token: "aabb"})
	if err != nil || joined.LogID != "log-1" {
		t.Fatalf("JoinSharedLog() = %#v, %v", joined, err)
	}

	if store.deletedLogID != "log-1" || store.listedLogID != "log-1" || store.removedLogID != "log-1" || store.removedShareID != "share-1" {
		t.Fatalf("unexpected store calls: %#v", store)
	}
	if hex.EncodeToString(store.infoToken) != "0102" || hex.EncodeToString(store.joinToken) != "aabb" {
		t.Fatalf("decoded tokens = %x, %x", store.infoToken, store.joinToken)
	}
}

func TestSharingActionsRejectInvalidLinksAndPropagateSentinels(t *testing.T) {
	store := &fakeSharingStore{}
	app := New(Config{Sharing: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := GetShareInfo.Call(ctx, app, GetShareInfoParams{Token: "not-hex"}); !errors.Is(err, ErrInvalidShareLink) {
		t.Fatalf("GetShareInfo error = %v", err)
	}
	if _, err := JoinSharedLog.Call(ctx, app, JoinSharedLogParams{}); !errors.Is(err, ErrInvalidShareLink) {
		t.Fatalf("JoinSharedLog error = %v", err)
	}
	if store.infoToken != nil || store.joinToken != nil {
		t.Fatal("invalid tokens reached the store")
	}

	store.err = ErrAlreadyOwnLog
	if _, err := JoinSharedLog.Call(ctx, app, JoinSharedLogParams{Token: "01"}); !errors.Is(err, ErrAlreadyOwnLog) {
		t.Fatalf("sentinel error = %v", err)
	}
	if _, err := ListSharedUsers.Call(context.Background(), app, ListSharedUsersParams{}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unauthenticated error = %v", err)
	}
}
