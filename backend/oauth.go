package backend

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/httplog/v3"
	"github.com/gofrs/uuid/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	fositeoauth2 "github.com/ory/fosite/handler/oauth2"
)

const (
	oauthScopeMCP            = "mcp"
	oauthAccessTokenLifespan = time.Hour
	oauthRefreshTokenLifespan = 30 * 24 * time.Hour
	oauthAuthorizeCodeLifespan = 5 * time.Minute
)

// errInvalidTarget is the RFC 8707 error code "invalid_target" for resource
// indicator validation failures. Fosite doesn't ship this error, so we
// construct it manually as an RFC 6749-shaped error with the right name.
var errInvalidTarget = &fosite.RFC6749Error{
	ErrorField:       "invalid_target",
	DescriptionField: "The requested resource is invalid, missing, unknown, or malformed.",
	CodeField:        http.StatusBadRequest,
}

// oauthProvider bundles the fosite provider, our storage, and the canonical
// URL of this MCP server (used as both the OAuth issuer and the RFC 8707
// audience for issued access tokens).
type oauthProvider struct {
	provider     fosite.OAuth2Provider
	storage      *oauthStorage
	canonicalURL string // e.g. "https://logger4life.example.com"
	hmacSecret   []byte
}

// newOAuthProvider constructs the OAuth provider. The HMAC secret is the
// key fosite uses to sign opaque tokens; it is generated once per process,
// which means a server restart invalidates all outstanding access tokens.
// Refresh tokens kept in DB will also become unusable until reissued. For
// now this is acceptable; persisting the secret is a follow-up.
func newOAuthProvider(pool *pgxpool.Pool, canonicalURL string) (*oauthProvider, error) {
	storage := newOAuthStorage(pool)

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate hmac secret: %w", err)
	}

	cfg := &fosite.Config{
		AccessTokenLifespan:           oauthAccessTokenLifespan,
		RefreshTokenLifespan:          oauthRefreshTokenLifespan,
		AuthorizeCodeLifespan:         oauthAuthorizeCodeLifespan,
		ScopeStrategy:                 fosite.HierarchicScopeStrategy,
		AudienceMatchingStrategy:      fosite.DefaultAudienceMatchingStrategy,
		EnforcePKCE:                   true,
		EnforcePKCEForPublicClients:   true,
		EnablePKCEPlainChallengeMethod: false,
		GlobalSecret:                  secret,
		RefreshTokenScopes:            []string{},
		// Returns the underlying error reason to OAuth clients in the
		// error_description field. Useful while integrating; consider turning
		// off in production once the flow is stable.
		SendDebugMessagesToClients: true,
	}

	strategy := compose.NewOAuth2HMACStrategy(cfg)

	provider := compose.Compose(
		cfg, storage, strategy,
		compose.OAuth2AuthorizeExplicitFactory,
		compose.OAuth2RefreshTokenGrantFactory,
		compose.OAuth2TokenRevocationFactory,
		compose.OAuth2TokenIntrospectionFactory,
		compose.OAuth2PKCEFactory,
	)

	return &oauthProvider{
		provider:     provider,
		storage:      storage,
		canonicalURL: canonicalURL,
		hmacSecret:   secret,
	}, nil
}

// ===== Discovery / metadata endpoints =====

// handleProtectedResourceMetadata serves RFC 9728. The MCP client uses this
// to discover where to obtain access tokens for our /mcp endpoint.
func (p *oauthProvider) handleProtectedResourceMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"resource":              p.canonicalURL,
			"authorization_servers": []string{p.canonicalURL},
			"scopes_supported":      []string{oauthScopeMCP},
			"bearer_methods_supported": []string{"header"},
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, http.StatusOK, body)
	}
}

// handleAuthorizationServerMetadata serves RFC 8414. The MCP client uses
// this to learn about our authorize/token/registration endpoints.
func (p *oauthProvider) handleAuthorizationServerMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"issuer":                                p.canonicalURL,
			"authorization_endpoint":                p.canonicalURL + "/oauth/authorize",
			"token_endpoint":                        p.canonicalURL + "/oauth/token",
			"registration_endpoint":                 p.canonicalURL + "/oauth/register",
			"revocation_endpoint":                   p.canonicalURL + "/oauth/revoke",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
			"scopes_supported":                      []string{oauthScopeMCP},
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, http.StatusOK, body)
	}
}

// ===== Dynamic Client Registration (RFC 7591) =====

type dcrRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	LogoURI                 string   `json:"logo_uri,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

type dcrResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientName              string   `json:"client_name,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	LogoURI                 string   `json:"logo_uri,omitempty"`
	Scope                   string   `json:"scope"`
}

func (p *oauthProvider) handleDynamicClientRegistration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dcrRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeOAuthRegistrationError(w, "invalid_client_metadata", "could not parse request body")
			return
		}
		if len(req.RedirectURIs) == 0 {
			writeOAuthRegistrationError(w, "invalid_redirect_uri", "redirect_uris is required")
			return
		}
		for _, u := range req.RedirectURIs {
			if !isValidRedirectURI(u) {
				writeOAuthRegistrationError(w, "invalid_redirect_uri", "redirect_uri must be https or http://localhost")
				return
			}
		}

		grants := req.GrantTypes
		if len(grants) == 0 {
			grants = []string{"authorization_code", "refresh_token"}
		}
		responseTypes := req.ResponseTypes
		if len(responseTypes) == 0 {
			responseTypes = []string{"code"}
		}

		clientID, err := uuid.NewV7()
		if err != nil {
			internalError(w, r, err)
			return
		}

		dc := &fosite.DefaultClient{
			ID:            clientID.String(),
			RedirectURIs:  req.RedirectURIs,
			GrantTypes:    grants,
			ResponseTypes: responseTypes,
			Scopes:        []string{oauthScopeMCP},
			Audience:      []string{p.canonicalURL},
			Public:        true,
		}
		if err := p.storage.createClient(r.Context(), dc, req.ClientName, req.ClientURI, req.LogoURI); err != nil {
			internalError(w, r, err)
			return
		}

		writeJSON(w, http.StatusCreated, dcrResponse{
			ClientID:                dc.ID,
			ClientIDIssuedAt:        time.Now().Unix(),
			RedirectURIs:            dc.RedirectURIs,
			GrantTypes:              dc.GrantTypes,
			ResponseTypes:           dc.ResponseTypes,
			TokenEndpointAuthMethod: "none",
			ClientName:              req.ClientName,
			ClientURI:               req.ClientURI,
			LogoURI:                 req.LogoURI,
			Scope:                   oauthScopeMCP,
		})
	}
}

func writeOAuthRegistrationError(w http.ResponseWriter, code, desc string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{
		"error":             code,
		"error_description": desc,
	})
}

func isValidRedirectURI(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1") {
		return true
	}
	return false
}

// ===== Authorize endpoint =====
//
// Flow:
//   GET  /oauth/authorize?... → if no session, 302 to /login?return_to=...
//                              → otherwise validate request, render consent page
//   POST /oauth/authorize     → form re-includes original query params + an
//                              approve flag. We re-validate, then mint a
//                              code (or redirect with access_denied).

func (p *oauthProvider) handleAuthorize() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Fosite reads request fields from r.Form, which ParseForm populates
		// from query + body.
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		ar, err := p.provider.NewAuthorizeRequest(ctx, r)
		if err != nil {
			httplog.SetError(ctx, err)
			p.provider.WriteAuthorizeError(ctx, w, ar, err)
			return
		}

		// Audience binding (RFC 8707): the resource parameter must match our
		// canonical URL. If absent, bind to ourselves anyway — the MCP spec
		// says clients MUST send it, but accepting omission keeps things
		// debuggable from curl.
		resource := r.Form.Get("resource")
		if resource != "" && !sameCanonicalURL(resource, p.canonicalURL) {
			err := errInvalidTarget.WithHintf("resource parameter %q does not match this server", resource)
			p.provider.WriteAuthorizeError(ctx, w, ar, err)
			return
		}
		ar.GrantAudience(p.canonicalURL)

		// Ensure the requested scope is supported. We only offer "mcp".
		for _, s := range ar.GetRequestedScopes() {
			if s != oauthScopeMCP {
				err := fosite.ErrInvalidScope.WithHintf("unsupported scope %q", s)
				p.provider.WriteAuthorizeError(ctx, w, ar, err)
				return
			}
			ar.GrantScope(s)
		}
		// If the client didn't request any scope, grant the default MCP scope
		// so they get usable tokens.
		if len(ar.GetGrantedScopes()) == 0 {
			ar.GrantScope(oauthScopeMCP)
		}

		user := userFromContext(ctx)
		if user == nil {
			// Bounce through login. Preserve everything via return_to.
			returnTo := "/oauth/authorize?" + r.Form.Encode()
			http.Redirect(w, r, "/login?return_to="+url.QueryEscape(returnTo), http.StatusSeeOther)
			return
		}

		approved := r.Method == http.MethodPost && r.Form.Get("approve") == "true"
		denied := r.Method == http.MethodPost && r.Form.Get("approve") == "false"

		if denied {
			err := fosite.ErrAccessDenied.WithHint("user denied the request")
			p.provider.WriteAuthorizeError(ctx, w, ar, err)
			return
		}

		if !approved {
			// First arrival (GET) or POST with no decision: render consent.
			renderConsentPage(w, r, consentData{
				Username:     user.Username,
				ClientID:     ar.GetClient().GetID(),
				RedirectURI:  r.Form.Get("redirect_uri"),
				Scopes:       ar.GetRequestedScopes(),
				FormFields:   r.Form,
			})
			return
		}

		// Approved: bind the user identity into the session and mint the code.
		sess := newMCPSession()
		sess.Subject = user.ID
		sess.Username = user.Username
		sess.UserID = user.ID
		sess.Extra = map[string]any{}

		response, err := p.provider.NewAuthorizeResponse(ctx, ar, sess)
		if err != nil {
			httplog.SetError(ctx, err)
			p.provider.WriteAuthorizeError(ctx, w, ar, err)
			return
		}
		p.provider.WriteAuthorizeResponse(ctx, w, ar, response)
	}
}

// ===== Token endpoint =====

func (p *oauthProvider) handleToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sess := newMCPSession()
		ar, err := p.provider.NewAccessRequest(ctx, r, sess)
		if err != nil {
			httplog.SetError(ctx, err)
			p.provider.WriteAccessError(ctx, w, ar, err)
			return
		}

		// RFC 8707 audience check: if the client passes a resource on the
		// token request, it must match what was approved on the authorize
		// request (and our canonical URL).
		if resource := r.Form.Get("resource"); resource != "" {
			if !sameCanonicalURL(resource, p.canonicalURL) {
				err := errInvalidTarget.WithHintf("resource parameter %q does not match this server", resource)
				p.provider.WriteAccessError(ctx, w, ar, err)
				return
			}
		}

		response, err := p.provider.NewAccessResponse(ctx, ar)
		if err != nil {
			httplog.SetError(ctx, err)
			p.provider.WriteAccessError(ctx, w, ar, err)
			return
		}
		p.provider.WriteAccessResponse(ctx, w, ar, response)
	}
}

// ===== Revocation endpoint =====

func (p *oauthProvider) handleRevoke() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		err := p.provider.NewRevocationRequest(ctx, r)
		if err != nil {
			httplog.SetError(ctx, err)
		}
		p.provider.WriteRevocationResponse(ctx, w, err)
	}
}

// ===== Bearer token verification (used by the MCP middleware) =====
//
// Returns the AuthUser bound to a valid access token whose audience matches
// our canonical URL. Returns an error otherwise; the caller turns errors
// into 401 Unauthorized + WWW-Authenticate.
func (p *oauthProvider) verifyAccessToken(ctx context.Context, token string) (*AuthUser, error) {
	sess := newMCPSession()
	tokenType, ar, err := p.provider.IntrospectToken(ctx, token, fosite.AccessToken, sess)
	if err != nil {
		return nil, err
	}
	if tokenType != fosite.AccessToken {
		return nil, errors.New("token is not an access token")
	}

	// Audience must include our canonical URL (RFC 8707).
	audienceOK := false
	for _, a := range ar.GetGrantedAudience() {
		if sameCanonicalURL(a, p.canonicalURL) {
			audienceOK = true
			break
		}
	}
	if !audienceOK {
		return nil, errors.New("access token audience does not match this MCP server")
	}

	mcpSess, ok := ar.GetSession().(*MCPSession)
	if !ok || mcpSess.UserID == "" {
		return nil, errors.New("access token has no user binding")
	}
	return &AuthUser{ID: mcpSess.UserID, Username: mcpSess.Username}, nil
}

// sameCanonicalURL compares two canonical URL strings for OAuth audience
// matching, tolerating trailing slashes and case differences in scheme/host.
func sameCanonicalURL(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}

// Compile-time interface assertions: catches drift if fosite changes its
// storage interfaces.
var (
	_ fosite.Storage                  = (*oauthStorage)(nil)
	_ fositeoauth2.AuthorizeCodeStorage = (*oauthStorage)(nil)
	_ fositeoauth2.AccessTokenStorage   = (*oauthStorage)(nil)
	_ fositeoauth2.RefreshTokenStorage  = (*oauthStorage)(nil)
	_ fositeoauth2.TokenRevocationStorage = (*oauthStorage)(nil)
)
