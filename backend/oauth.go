package backend

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/httplog/v3"
	"github.com/gofrs/uuid/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Hand-rolled OAuth 2.1 authorization server, scoped to exactly what the
// MCP integration needs: authorization-code grant with PKCE S256, refresh
// tokens, RFC 7591 dynamic client registration, RFC 8414 / RFC 9728
// metadata, RFC 8707 audience binding. No OIDC, no JWT, no client_secret.

const (
	oauthScopeMCP            = "mcp"
	oauthAccessTokenLifespan = time.Hour
	oauthRefreshTokenLifespan = 30 * 24 * time.Hour
	oauthAuthorizationCodeLifespan = 5 * time.Minute
	oauthAccessTokenPrefix   = "l4l_at_"
	oauthRefreshTokenPrefix  = "l4l_rt_"
	oauthAuthorizationCodePrefix = "l4l_ac_"
)

type oauthProvider struct {
	pool         *pgxpool.Pool
	canonicalURL string
}

func newOAuthProvider(pool *pgxpool.Pool, canonicalURL string) *oauthProvider {
	return &oauthProvider{pool: pool, canonicalURL: strings.TrimRight(canonicalURL, "/")}
}

// ===== Token generation =====
//
// Tokens are 32 bytes of CSPRNG output, base64url-encoded, prefixed for
// human readability. Persistence stores only sha256(token).

func generateToken(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// ===== PKCE =====

// verifyPKCE recomputes the S256 challenge from the verifier and compares it
// to the challenge the client committed to at /authorize. Constant-time
// compare to avoid leaking timing info on the verifier.
func verifyPKCE(challenge, method, verifier string) bool {
	if method != "S256" {
		return false
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// ===== Discovery / metadata endpoints =====

func (p *oauthProvider) handleProtectedResourceMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, http.StatusOK, map[string]any{
			"resource":                 p.canonicalURL,
			"authorization_servers":    []string{p.canonicalURL},
			"scopes_supported":         []string{oauthScopeMCP},
			"bearer_methods_supported": []string{"header"},
		})
	}
}

func (p *oauthProvider) handleAuthorizationServerMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, http.StatusOK, map[string]any{
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
		})
	}
}

// ===== Dynamic Client Registration (RFC 7591) =====

type dcrRequest struct {
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name,omitempty"`
}

type dcrResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientName              string   `json:"client_name,omitempty"`
	Scope                   string   `json:"scope"`
}

func (p *oauthProvider) handleDynamicClientRegistration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dcrRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "could not parse request body")
			return
		}
		if len(req.RedirectURIs) == 0 {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
			return
		}
		for _, u := range req.RedirectURIs {
			if !isValidRedirectURI(u) {
				writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri must be https or http://localhost")
				return
			}
		}

		clientID, err := uuid.NewV7()
		if err != nil {
			internalError(w, r, err)
			return
		}
		c := oauthClient{
			ID:           clientID.String(),
			RedirectURIs: req.RedirectURIs,
			ClientName:   req.ClientName,
		}
		if err := createOAuthClient(r.Context(), p.pool, c); err != nil {
			internalError(w, r, err)
			return
		}

		writeJSON(w, http.StatusCreated, dcrResponse{
			ClientID:                c.ID,
			ClientIDIssuedAt:        time.Now().Unix(),
			RedirectURIs:            c.RedirectURIs,
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
			ClientName:              c.ClientName,
			Scope:                   oauthScopeMCP,
		})
	}
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
//   GET /oauth/authorize?...
//     - validate everything we can without a session
//     - if no session cookie, 302 to /login?return_to=...
//     - otherwise render consent page with hidden form fields for all params
//   POST /oauth/authorize
//     - re-parse + re-validate from form body
//     - if approve=true, mint code and 302 to redirect_uri with code
//     - if approve=false, 302 to redirect_uri with error=access_denied

type authorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
}

func parseAuthorizeParams(values url.Values) authorizeParams {
	return authorizeParams{
		ResponseType:        values.Get("response_type"),
		ClientID:            values.Get("client_id"),
		RedirectURI:         values.Get("redirect_uri"),
		Scope:               values.Get("scope"),
		State:               values.Get("state"),
		CodeChallenge:       values.Get("code_challenge"),
		CodeChallengeMethod: values.Get("code_challenge_method"),
		Resource:            values.Get("resource"),
	}
}

func (p *oauthProvider) handleAuthorize() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		params := parseAuthorizeParams(r.Form)

		// Look up the client first; we need its registered redirect URIs
		// to know whether it is safe to redirect errors back to.
		client, err := getOAuthClient(ctx, p.pool, params.ClientID)
		if err != nil {
			// Cannot redirect — render the error inline.
			writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
			return
		}
		if !redirectURIRegistered(client, params.RedirectURI) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri does not match a registered URI")
			return
		}

		// From here onward, errors redirect back to the client per OAuth 2.1.
		if params.ResponseType != "code" {
			redirectAuthorizeError(w, r, params, "unsupported_response_type", "only response_type=code is supported")
			return
		}
		if params.CodeChallenge == "" || params.CodeChallengeMethod != "S256" {
			redirectAuthorizeError(w, r, params, "invalid_request", "PKCE S256 code_challenge is required")
			return
		}
		if params.State == "" || len(params.State) < 8 {
			redirectAuthorizeError(w, r, params, "invalid_request", "state is required and must be at least 8 characters")
			return
		}
		if params.Scope == "" {
			params.Scope = oauthScopeMCP
		}
		for _, s := range strings.Fields(params.Scope) {
			if s != oauthScopeMCP {
				redirectAuthorizeError(w, r, params, "invalid_scope", "unsupported scope "+s)
				return
			}
		}
		// RFC 8707 audience binding: the resource MUST identify this server.
		if params.Resource != "" && !sameCanonicalURL(params.Resource, p.canonicalURL) {
			redirectAuthorizeError(w, r, params, "invalid_target", "resource parameter does not match this server")
			return
		}

		user := userFromContext(ctx)
		if user == nil {
			returnTo := "/oauth/authorize?" + r.Form.Encode()
			http.Redirect(w, r, "/login?return_to="+url.QueryEscape(returnTo), http.StatusSeeOther)
			return
		}

		approved := r.Method == http.MethodPost && r.Form.Get("approve") == "true"
		denied := r.Method == http.MethodPost && r.Form.Get("approve") == "false"

		if denied {
			redirectAuthorizeError(w, r, params, "access_denied", "user denied the request")
			return
		}
		if !approved {
			renderConsentPage(w, r, consentData{
				Username:    user.Username,
				ClientID:    params.ClientID,
				RedirectURI: params.RedirectURI,
				Scopes:      strings.Fields(params.Scope),
				FormFields:  r.Form,
			})
			return
		}

		// Approved: mint a code.
		code, err := generateToken(oauthAuthorizationCodePrefix)
		if err != nil {
			internalError(w, r, err)
			return
		}
		audience := params.Resource
		if audience == "" {
			audience = p.canonicalURL
		}
		if err := createAuthorizationCode(ctx, p.pool, code, oauthAuthorizationCode{
			ClientID:            params.ClientID,
			UserID:              user.ID,
			RedirectURI:         params.RedirectURI,
			Scope:               params.Scope,
			Audience:            audience,
			CodeChallenge:       params.CodeChallenge,
			CodeChallengeMethod: params.CodeChallengeMethod,
			ExpiresAt:           time.Now().Add(oauthAuthorizationCodeLifespan),
		}); err != nil {
			internalError(w, r, err)
			return
		}

		redirectAuthorizeSuccess(w, r, params, code)
	}
}

func redirectURIRegistered(c *oauthClient, redirectURI string) bool {
	for _, u := range c.RedirectURIs {
		if u == redirectURI {
			return true
		}
	}
	return false
}

func redirectAuthorizeSuccess(w http.ResponseWriter, r *http.Request, p authorizeParams, code string) {
	u, _ := url.Parse(p.RedirectURI)
	q := u.Query()
	q.Set("code", code)
	q.Set("state", p.State)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

func redirectAuthorizeError(w http.ResponseWriter, r *http.Request, p authorizeParams, code, desc string) {
	httplog.SetError(r.Context(), errors.New(code+": "+desc))
	if p.RedirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, code, desc)
		return
	}
	u, err := url.Parse(p.RedirectURI)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, code, desc)
		return
	}
	q := u.Query()
	q.Set("error", code)
	q.Set("error_description", desc)
	if p.State != "" {
		q.Set("state", p.State)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// ===== Token endpoint =====

func (p *oauthProvider) handleToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "could not parse form body")
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			p.tokenAuthCodeGrant(w, r)
		case "refresh_token":
			p.tokenRefreshGrant(w, r)
		default:
			writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		}
	}
}

func (p *oauthProvider) tokenAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clientID := r.Form.Get("client_id")
	code := r.Form.Get("code")
	redirectURI := r.Form.Get("redirect_uri")
	verifier := r.Form.Get("code_verifier")
	resource := r.Form.Get("resource")

	if clientID == "" || code == "" || verifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id, code, and code_verifier are required")
		return
	}

	// Look up + atomically consume the code.
	stored, err := consumeAuthorizationCode(ctx, p.pool, code)
	if err != nil {
		// Either unknown code or already-used code. Treat both as
		// invalid_grant; do not distinguish to the client.
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code is invalid, expired, or already used")
		return
	}
	if stored.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code was issued to a different client")
		return
	}
	if stored.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the original request")
		return
	}
	if !verifyPKCE(stored.CodeChallenge, stored.CodeChallengeMethod, verifier) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	if resource != "" && !sameCanonicalURL(resource, stored.Audience) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource parameter does not match the original request")
		return
	}

	tokens, err := p.issueTokenPair(ctx, oauthAccessTokenRow{
		ClientID: stored.ClientID,
		UserID:   stored.UserID,
		Scope:    stored.Scope,
		Audience: stored.Audience,
	})
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeTokenResponse(w, tokens, stored.Scope)
}

func (p *oauthProvider) tokenRefreshGrant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clientID := r.Form.Get("client_id")
	refresh := r.Form.Get("refresh_token")
	resource := r.Form.Get("resource")

	if clientID == "" || refresh == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id and refresh_token are required")
		return
	}

	stored, err := consumeRefreshToken(ctx, p.pool, refresh)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh_token is invalid, expired, or revoked")
		return
	}
	if stored.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh_token was issued to a different client")
		return
	}
	if resource != "" && !sameCanonicalURL(resource, stored.Audience) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource parameter does not match the original request")
		return
	}

	tokens, err := p.issueTokenPair(ctx, *stored)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeTokenResponse(w, tokens, stored.Scope)
}

func (p *oauthProvider) issueTokenPair(ctx context.Context, info oauthAccessTokenRow) (oauthTokens, error) {
	access, err := generateToken(oauthAccessTokenPrefix)
	if err != nil {
		return oauthTokens{}, err
	}
	refresh, err := generateToken(oauthRefreshTokenPrefix)
	if err != nil {
		return oauthTokens{}, err
	}
	now := time.Now()
	t := oauthTokens{
		AccessToken:      access,
		RefreshToken:     refresh,
		AccessExpiresAt:  now.Add(oauthAccessTokenLifespan),
		RefreshExpiresAt: now.Add(oauthRefreshTokenLifespan),
	}
	if err := persistTokenPair(ctx, p.pool, t, info); err != nil {
		return oauthTokens{}, err
	}
	return t, nil
}

func writeTokenResponse(w http.ResponseWriter, t oauthTokens, scope string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  t.AccessToken,
		"refresh_token": t.RefreshToken,
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessTokenLifespan.Seconds()),
		"scope":         scope,
	})
}

// ===== Revocation endpoint (RFC 7009) =====

func (p *oauthProvider) handleRevoke() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "could not parse form body")
			return
		}
		token := r.Form.Get("token")
		if token == "" {
			// Per RFC 7009 §2.2, return 200 even when the token is unknown.
			w.WriteHeader(http.StatusOK)
			return
		}
		hint := r.Form.Get("token_type_hint")
		// Try the hinted type first, then fall back. Spec allows either.
		switch hint {
		case "refresh_token":
			revokeRefreshToken(r.Context(), p.pool, token)
			revokeAccessToken(r.Context(), p.pool, token)
		default:
			revokeAccessToken(r.Context(), p.pool, token)
			revokeRefreshToken(r.Context(), p.pool, token)
		}
		w.WriteHeader(http.StatusOK)
	}
}

// ===== Bearer verification (used by MCP middleware) =====

func (p *oauthProvider) verifyAccessToken(ctx context.Context, token string) (*AuthUser, error) {
	row, err := lookupAccessToken(ctx, p.pool, token)
	if err != nil {
		return nil, errors.New("invalid or expired access token")
	}
	if !sameCanonicalURL(row.Audience, p.canonicalURL) {
		return nil, errors.New("access token audience does not match this MCP server")
	}
	return &AuthUser{ID: row.UserID, Username: row.Username}, nil
}

// ===== Helpers =====

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{
		"error":             code,
		"error_description": desc,
	})
}

// sameCanonicalURL compares two canonical URL strings for OAuth audience
// matching, tolerating trailing slashes and case differences in scheme/host.
func sameCanonicalURL(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}
