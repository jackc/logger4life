package cimd

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

var errMetadata = errors.New("invalid client metadata document")

func parseMetadata(id string, body []byte) (core.OAuthClient, error) {
	// A map preserves exact member names and permits metadata extensions.
	// JSON v2 rejects duplicates, invalid Unicode, and trailing input.
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return core.OAuthClient{}, errMetadata
	}
	var documentID, name, authMethod string
	var redirects []string
	if json.Unmarshal(fields["client_id"], &documentID) != nil || documentID != id ||
		json.Unmarshal(fields["client_name"], &name) != nil || strings.TrimSpace(name) == "" || len(name) > core.OAuthMaxClientNameBytes ||
		json.Unmarshal(fields["redirect_uris"], &redirects) != nil || len(redirects) == 0 || len(redirects) > core.OAuthMaxRedirectURIs ||
		json.Unmarshal(fields["token_endpoint_auth_method"], &authMethod) != nil || authMethod != "none" {
		return core.OAuthClient{}, errMetadata
	}
	for _, redirect := range redirects {
		if len(redirect) > core.OAuthMaxRedirectURIBytes || !domain.ValidRedirectURI(redirect) {
			return core.OAuthClient{}, errMetadata
		}
	}
	// Only public clients are supported. Reject secrets and embedded keys;
	// never silently downgrade a confidential client to unauthenticated use.
	for _, key := range []string{"client_secret", "client_secret_expires_at", "jwks"} {
		if _, exists := fields[key]; exists {
			return core.OAuthClient{}, errMetadata
		}
	}
	grants := []string{"authorization_code"} // RFC 7591 default
	if raw, exists := fields["grant_types"]; exists {
		if json.Unmarshal(raw, &grants) != nil || !slices.Contains(grants, "authorization_code") {
			return core.OAuthClient{}, errMetadata
		}
	}
	for _, grant := range grants {
		if grant != "authorization_code" && grant != "refresh_token" {
			return core.OAuthClient{}, errMetadata
		}
	}
	if raw, exists := fields["response_types"]; exists {
		var responses []string
		if json.Unmarshal(raw, &responses) != nil || len(responses) != 1 || responses[0] != "code" {
			return core.OAuthClient{}, errMetadata
		}
	}
	if raw, exists := fields["scope"]; exists {
		var scope string
		if json.Unmarshal(raw, &scope) != nil || strings.TrimSpace(scope) != core.OAuthScopeMCP {
			return core.OAuthClient{}, errMetadata
		}
	}
	return core.OAuthClient{ID: id, ClientName: name, RedirectURIs: redirects, AuthorizationCodeOnly: !slices.Contains(grants, "refresh_token")}, nil
}
