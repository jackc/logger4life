package core

import (
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
