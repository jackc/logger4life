package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// publicActions is the complete set a caller may invoke before being anyone.
// Every one of them either establishes who the caller is or belongs to a
// protocol exchange that runs before there is a session to speak of. Listing
// them here rather than only on the actions means opening a new one is a
// visible decision in a test a reviewer reads, not a field they might skim.
var publicActions = map[string]string{
	"register_user":               "creates the account, so there is nobody yet",
	"login_with_password":         "establishes the caller",
	"authenticate_session":        "resolves a session cookie into the caller",
	"logout":                      "takes a session token, and a caller with a stale one still has to be able to drop it",
	"begin_passkey_login":         "starts a ceremony for a caller who is not yet identified",
	"finish_passkey_login":        "establishes the caller from a verified credential",
	"register_oauth_client":       "RFC 7591 dynamic client registration is unauthenticated by design",
	"prepare_oauth_authorization": "validates a client's authorize request before the user consents",
	"exchange_oauth_code":         "the token endpoint authenticates the code, not a session",
	"refresh_oauth_token":         "the token endpoint authenticates the refresh token, not a session",
	"revoke_oauth_token":          "RFC 7009 revocation authenticates the token being revoked",
	"authenticate_oauth_token":    "resolves a bearer token into the caller",
	"get_sql_schema":              "describes the shared read-only views, not anybody's rows",
}

// Every other action must turn away a caller who is nobody, and must do it
// before saying anything about the parameters it was sent.
func TestEveryActionRefusesAnAnonymousCallerUnlessItIsPublic(t *testing.T) {
	app := New(Config{Middleware: []Middleware{RequireUser()}})

	for _, action := range Catalog() {
		if _, public := publicActions[action.Name()]; public {
			continue
		}
		// Zero parameters: an anonymous caller must be refused whatever it
		// sent, so nothing has to be filled in for this to be a fair test.
		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panicked on a nil port: %v", r)
				}
			}()
			_, err = action.Invoke(context.Background(), app, action.NewParams())
			return err
		}()
		if !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s answered an anonymous caller with %v, want ErrUnauthenticated; mark it Public if that is intended", action.Name(), err)
		}
	}
}

// The list and the declarations have to agree, so that an action cannot be
// opened by editing one without the other.
func TestPublicActionsAreExactlyTheOnesDeclaredPublic(t *testing.T) {
	declared := map[string]bool{}
	for _, action := range Catalog() {
		if action.Public() {
			declared[action.Name()] = true
		}
		if _, listed := publicActions[action.Name()]; listed != action.Public() {
			t.Errorf("%s: declared Public = %t, listed as public = %t", action.Name(), action.Public(), listed)
		}
	}
	for name := range publicActions {
		if !declared[name] {
			t.Errorf("%s is listed as public but no such action declares itself Public", name)
		}
	}
}

// Without the middleware the actions still refuse an anonymous caller
// themselves, which is what makes the middleware a second line rather than the
// only one. Each action group's own tests cover this for its actions — see the
// "RequireAuthenticationBeforeCallingStore" tests — because doing it across the
// whole catalog would need valid parameters for every action, parameter
// validation running ahead of the handler.
func TestActionsRefuseAnAnonymousCallerWithoutTheMiddleware(t *testing.T) {
	app := New(Config{})
	if _, err := ListLogs.Call(context.Background(), app, ListLogsParams{}); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("list_logs without the middleware = %v, want ErrUnauthenticated", err)
	}
}
