// Package dualstore runs every persistence call against PostgreSQL and jed,
// returns PostgreSQL's result, and panics on any observable divergence. It is
// a validation harness: fail-stop behavior is intentional.
package dualstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/logger4life/backend/core"
)

// Backend is one complete implementation of core's persistence ports.
type Backend interface {
	core.UserStore
	core.SessionStore
	core.PasskeyStore
	core.PasskeyChallengeStore
	core.LogStore
	core.LogEntryStore
	core.LogPlacementStore
	core.FolderStore
	core.SavedQueryStore
	core.SQLSchemaStore
	core.UserSQLExecutor
	core.SharingStore
	core.OAuthStore
	core.Transactor
}

// Store compares primary and secondary. Successful primary results are the
// ones exposed to the application.
type Store struct {
	primary   Backend
	secondary Backend
}

func New(primary, secondary Backend) *Store {
	return &Store{primary: primary, secondary: secondary}
}

// InTx nests the secondary transaction inside the primary transaction. Each
// adapter uses its own context key, so calls made by fn participate in both.
func (s *Store) InTx(ctx context.Context, fn func(context.Context) error) error {
	return s.primary.InTx(ctx, func(ctx context.Context) error {
		return s.secondary.InTx(ctx, fn)
	})
}

var sentinels = []error{
	core.ErrUsernameTaken,
	core.ErrEmailTaken,
	core.ErrUserNotFound,
	core.ErrInvalidSession,
	core.ErrLogNameTaken,
	core.ErrLogNotFound,
	core.ErrLogEntryNotFound,
	core.ErrFolderNotFound,
	core.ErrParentFolderNotFound,
	core.ErrFolderCycle,
	core.ErrFolderOwnParent,
	core.ErrFolderNotEmpty,
	core.ErrLogNotPinned,
	core.ErrSavedQueryNotFound,
	core.ErrSavedQueryNameTaken,
	core.ErrShareNotFound,
	core.ErrInvalidShareLink,
	core.ErrAlreadyOwnLog,
	core.ErrPasskeyNotFound,
	core.ErrPasskeyAlreadyRegistered,
	core.ErrInvalidPasskeyChallenge,
	core.ErrOAuthRecordNotFound,
	core.ErrOAuthRefreshReuse,
}

func sentinelOf(err error) error {
	for _, sentinel := range sentinels {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return nil
}

func check(method string, primaryValue, secondaryValue any, primaryErr, secondaryErr error) {
	if (primaryErr == nil) != (secondaryErr == nil) {
		panic(fmt.Sprintf("dualstore: %s diverged: primary err=%v, secondary err=%v", method, primaryErr, secondaryErr))
	}
	if primaryErr != nil {
		primarySentinel, secondarySentinel := sentinelOf(primaryErr), sentinelOf(secondaryErr)
		if primarySentinel != secondarySentinel || !equalUserSQLFailures(primaryErr, secondaryErr) {
			panic(fmt.Sprintf("dualstore: %s diverged: primary err=%v, secondary err=%v", method, primaryErr, secondaryErr))
		}
		return
	}
	if !equalValues(primaryValue, secondaryValue) {
		panic(fmt.Sprintf("dualstore: %s diverged: primary=%+v, secondary=%+v", method, primaryValue, secondaryValue))
	}
}

func compareCall[T any](method string, primary, secondary func() (T, error)) (T, error) {
	primaryValue, primaryErr := primary()
	secondaryValue, secondaryErr := secondary()
	check(method, primaryValue, secondaryValue, primaryErr, secondaryErr)
	return primaryValue, primaryErr
}

func compareError(method string, primary, secondary func() error) error {
	primaryErr := primary()
	secondaryErr := secondary()
	check(method, struct{}{}, struct{}{}, primaryErr, secondaryErr)
	return primaryErr
}

func equalUserSQLFailures(primaryErr, secondaryErr error) bool {
	var primaryFailure, secondaryFailure *core.UserSQLFailure
	primaryIsFailure := errors.As(primaryErr, &primaryFailure)
	secondaryIsFailure := errors.As(secondaryErr, &secondaryFailure)
	if primaryIsFailure != secondaryIsFailure {
		return false
	}
	return !primaryIsFailure || (primaryFailure.Kind == secondaryFailure.Kind && primaryFailure.Message == secondaryFailure.Message)
}

var timeType = reflect.TypeOf(time.Time{})

// equalValues ignores database-maintained audit clocks while comparing every
// semantic value, including occurred_at and OAuth expiry times.
func equalValues(primary, secondary any) bool {
	return equalValue(reflect.ValueOf(primary), reflect.ValueOf(secondary), "")
}

func equalValue(primary, secondary reflect.Value, fieldName string) bool {
	if !primary.IsValid() || !secondary.IsValid() {
		return primary.IsValid() == secondary.IsValid()
	}
	if primary.Type() != secondary.Type() {
		return false
	}
	if fieldName == "CreatedAt" || fieldName == "UpdatedAt" || fieldName == "SharedAt" || fieldName == "ElapsedMs" {
		return true
	}
	if fieldName == "DataType" && primary.Kind() == reflect.String {
		return equivalentSQLType(primary.String(), secondary.String())
	}
	if primary.Type() == timeType {
		return primary.Interface().(time.Time).Equal(secondary.Interface().(time.Time))
	}
	switch primary.Kind() {
	case reflect.Pointer, reflect.Interface:
		if primary.IsNil() || secondary.IsNil() {
			return primary.IsNil() == secondary.IsNil()
		}
		return equalValue(primary.Elem(), secondary.Elem(), fieldName)
	case reflect.Slice:
		if primary.IsNil() != secondary.IsNil() {
			return false
		}
		fallthrough
	case reflect.Array:
		if primary.Len() != secondary.Len() {
			return false
		}
		for i := 0; i < primary.Len(); i++ {
			if !equalValue(primary.Index(i), secondary.Index(i), "") {
				return false
			}
		}
		return true
	case reflect.Struct:
		for i := 0; i < primary.NumField(); i++ {
			if !equalValue(primary.Field(i), secondary.Field(i), primary.Type().Field(i).Name) {
				return false
			}
		}
		return true
	case reflect.Map:
		if primary.IsNil() != secondary.IsNil() || primary.Len() != secondary.Len() {
			return false
		}
		for _, key := range primary.MapKeys() {
			secondaryValue := secondary.MapIndex(key)
			if !secondaryValue.IsValid() || !equalValue(primary.MapIndex(key), secondaryValue, "") {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(primary.Interface(), secondary.Interface())
	}
}

func equivalentSQLType(primary, secondary string) bool {
	if primary == secondary {
		return true
	}
	aliases := map[string]string{
		"varchar":                  "text",
		"character varying":        "text",
		"character varying(30)":    "text",
		"character varying(100)":   "text",
		"uuid":                     "text",
		"int2":                     "integer",
		"smallint":                 "integer",
		"int4":                     "integer",
		"int8":                     "integer",
		"bigint":                   "integer",
		"i64":                      "integer",
		"float4":                   "real",
		"float8":                   "double precision",
		"bool":                     "boolean",
		"timestamptz":              "timestamp with time zone",
		"timestamp with time zone": "timestamp with time zone",
		"_text":                    "text[]",
	}
	canonical := func(value string) string {
		if alias, ok := aliases[value]; ok {
			return alias
		}
		return value
	}
	return canonical(primary) == canonical(secondary)
}
