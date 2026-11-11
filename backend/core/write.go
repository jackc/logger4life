package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
)

var ErrIdempotencyConflict = errors.New("idempotency key was already used with different arguments")

// ClaimWrite must run in a transaction. It serializes competing claims for
// (user, operation, key); SaveWrite and the mutation commit in that same transaction.
// An empty result means the claim is new. Successful results persist across restarts.
type WriteStore interface {
	ClaimWrite(context.Context, string, string, string, string) (string, error)
	SaveWrite(context.Context, string, string, string, string) error
}

// RunWrite commits the mutation, readback, and optional retry receipt together.
// Keys are scoped to caller and operation. Failures never consume a key.
func RunWrite[P, R any](ctx context.Context, c *Core, operation, key string, input P, fn func(context.Context) (R, error)) (R, error) {
	var result R
	user, err := requiredUser(ctx)
	if err != nil {
		return result, err
	}
	if len(key) > 128 {
		return result, &ValidationError{Err: fmt.Errorf("idempotency_key must be at most 128 bytes")}
	}
	err = c.tx.InTx(ctx, func(ctx context.Context) error {
		if key != "" {
			if c.writes == nil {
				return errors.New("write store is not configured")
			}
			data, err := json.Marshal(input, json.Deterministic(true))
			if err != nil {
				return err
			}
			hash := sha256.Sum256(data)
			cached, err := c.writes.ClaimWrite(ctx, user, operation, key, hex.EncodeToString(hash[:]))
			if err != nil {
				return err
			}
			if cached != "" {
				return json.Unmarshal([]byte(cached), &result)
			}
		}
		var err error
		result, err = fn(ctx)
		if err != nil {
			return err
		}
		if key != "" {
			data, err := json.Marshal(result)
			if err != nil {
				return err
			}
			return c.writes.SaveWrite(ctx, user, operation, key, string(data))
		}
		return nil
	})
	return result, err
}
