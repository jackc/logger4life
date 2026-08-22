package core

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The caller's identity always arrives in the context, never in an action's
// parameters. An action that accepted a user ID would let any caller reaching
// the catalog — including the dynamic InvokeJSON path an adapter may expose —
// act as another account. start_session was exactly that shape before it was
// removed, so this guards the whole catalog rather than one action.
func TestNoActionAcceptsACallerSuppliedUserID(t *testing.T) {
	for _, action := range Catalog() {
		params := reflect.TypeOf(action.NewParams()).Elem()
		if params.Kind() != reflect.Struct {
			continue
		}
		for i := range params.NumField() {
			field := params.Field(i)
			tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if strings.EqualFold(field.Name, "userID") || tag == "user_id" {
				t.Errorf("action %q takes %s from its parameters; identity must come from the context", action.Name(), field.Name)
			}
		}
	}
}

// Every action is reachable by name. An action missing from the registry would
// be invisible to the catalog and to any dynamic caller.
func TestCatalogEntriesAreLookupable(t *testing.T) {
	catalog := Catalog()
	if len(catalog) == 0 {
		t.Fatal("the action catalog is empty")
	}
	for _, action := range catalog {
		found, ok := Lookup(action.Name())
		if !ok || found != action {
			t.Errorf("Lookup(%q) = %v, %t", action.Name(), found, ok)
		}
	}
}

// nonUUIDParams are the ID parameters that are legitimately not UUIDs, and so
// are exempt from TestActionsRejectMalformedIDs.
var nonUUIDParams = map[string]bool{
	// OAuth client IDs are issued by dynamic client registration and stored
	// in a text column, so no string is malformed for one.
	"client_id": true,
}

// Every other ID a caller supplies names a uuid column, so an action must
// refuse a malformed one rather than hand it down: PostgreSQL rejects the cast
// with error 22P02, which reached the client as a 500 and carried a database
// detail out past the store boundary.
//
// This walks the catalog instead of a fixed list, so a new action is covered
// the day it is added and a genuinely non-UUID ID has to be named in
// nonUUIDParams — which is the point, since that makes the exception visible.
func TestActionsRejectMalformedIDs(t *testing.T) {
	const validID = "00000000-0000-4000-8000-000000000000"

	for _, action := range Catalog() {
		paramsType := reflect.TypeOf(action.NewParams()).Elem()
		if paramsType.Kind() != reflect.Struct {
			continue
		}
		for i := range paramsType.NumField() {
			field := paramsType.Field(i)
			tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if nonUUIDParams[tag] || (tag != "id" && !strings.HasSuffix(tag, "_id")) {
				continue
			}

			// Only the field under test is malformed. Every other ID gets a
			// well-formed value so that its own check cannot fire first and
			// mask a field this action never validates.
			params := reflect.New(paramsType)
			for j := range paramsType.NumField() {
				other, _, _ := strings.Cut(paramsType.Field(j).Tag.Get("json"), ",")
				if j != i && !nonUUIDParams[other] && (other == "id" || strings.HasSuffix(other, "_id")) && paramsType.Field(j).Type.Kind() == reflect.String {
					params.Elem().Field(j).SetString(validID)
				}
			}
			malformed := "not-a-uuid"
			switch field.Type.Kind() {
			case reflect.String:
				params.Elem().Field(i).SetString(malformed)
			case reflect.Pointer:
				params.Elem().Field(i).Set(reflect.ValueOf(&malformed))
			default:
				t.Errorf("action %q takes %s as %s, which this test cannot fill", action.Name(), tag, field.Type)
				continue
			}

			// Every port is nil, so an action that fails to reject the ID
			// reports the panic rather than taking the whole binary down.
			err := func() (err error) {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("panicked on a nil port: %v", r)
					}
				}()
				_, err = action.Invoke(context.Background(), New(Config{}), params.Interface())
				return err
			}()

			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Errorf("%s with a malformed %s returned %v, want a ValidationError", action.Name(), tag, err)
				continue
			}
			if !strings.Contains(validationErr.Err.Error(), tag) {
				t.Errorf("%s rejected a malformed %s with %q, which does not name the field", action.Name(), tag, validationErr.Err)
			}
		}
	}
}
