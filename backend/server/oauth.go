package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/httplog/v3"
	"github.com/jackc/logger4life/backend/core"
)

// HTTP adapter for the hand-rolled OAuth 2.1 authorization server, scoped to
// exactly what the MCP integration needs: authorization-code grant with PKCE
// S256, refresh tokens, RFC 7591 dynamic client registration, RFC 8414 /
// RFC 9728 metadata, RFC 8707 audience binding. No OIDC, no JWT, no
// client_secret.
//
// Grant rules and storage live in the action catalog; this file owns only
// metadata documents, form parsing, redirects, the consent page, and the
// translation of core errors into OAuth error responses.

type oauthProvider struct {
	app             *core.Core
	canonicalURL    string
	registrationIPs *keyedRateLimiter
	registrations   *keyedRateLimiter
	trustedProxies  []netip.Prefix
}

func newOAuthProvider(app *core.Core, canonicalURL string) *oauthProvider {
	return &oauthProvider{app: app, canonicalURL: strings.TrimRight(canonicalURL, "/"), registrationIPs: newKeyedRateLimiter(5, 5), registrations: newKeyedRateLimiter(30, 10)}
}

// ===== Discovery / metadata endpoints =====

func (p *oauthProvider) handleProtectedResourceMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, http.StatusOK, map[string]any{
			"resource":                 p.canonicalURL,
			"authorization_servers":    []string{p.canonicalURL},
			"scopes_supported":         []string{core.OAuthScopeMCP},
			"bearer_methods_supported": []string{"header"},
		})
	}
}

func (p *oauthProvider) handleAuthorizationServerMetadata() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                                         p.canonicalURL,
			"authorization_endpoint":                         p.canonicalURL + "/oauth/authorize",
			"token_endpoint":                                 p.canonicalURL + "/oauth/token",
			"registration_endpoint":                          p.canonicalURL + "/oauth/register",
			"revocation_endpoint":                            p.canonicalURL + "/oauth/revoke",
			"response_types_supported":                       []string{"code"},
			"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":               []string{"S256"},
			"token_endpoint_auth_methods_supported":          []string{"none"},
			"scopes_supported":                               []string{core.OAuthScopeMCP},
			"authorization_response_iss_parameter_supported": true,
		})
	}
}

// ===== Dynamic Client Registration (RFC 7591) =====

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
		if retry := p.registrationIPs.allow(requestClientIP(r, p.trustedProxies)); retry > 0 {
			writeRateLimit(w, retry)
			return
		}
		if retry := p.registrations.allow("global"); retry > 0 {
			writeRateLimit(w, retry)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		decoder := json.NewDecoder(r.Body)
		var params core.RegisterOAuthClientParams
		err := decoder.Decode(&params)
		if err == nil {
			var trailing any
			if err = decoder.Decode(&trailing); err == io.EOF {
				err = nil
			} else if err == nil {
				err = errors.New("multiple JSON documents")
			}
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeOAuthError(w, http.StatusRequestEntityTooLarge, "invalid_client_metadata", "registration body exceeds 32 KiB")
			} else {
				writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "expected one JSON object")
			}
			return
		}
		client, err := core.RegisterOAuthClient.Call(r.Context(), p.app, params)
		if err != nil {
			writeOAuthActionError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, dcrResponse{
			ClientID:                client.ID,
			ClientIDIssuedAt:        time.Now().Unix(),
			RedirectURIs:            client.RedirectURIs,
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
			ClientName:              client.ClientName,
			Scope:                   core.OAuthScopeMCP,
		})
	}
}

// ===== Authorize endpoint =====
//
// Flow:
//   GET /oauth/authorize?...
//     - validate the request against the registered client
//     - if no session cookie, 302 to /login?return_to=...
//     - otherwise render consent page with hidden form fields for all params
//   POST /oauth/authorize
//     - re-parse + re-validate from form body
//     - if approve=true, mint code and 302 to redirect_uri with code
//     - if approve=false, 302 to redirect_uri with error=access_denied

func parseAuthorizeParams(values url.Values) core.OAuthAuthorizationParams {
	return core.OAuthAuthorizationParams{
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
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		values := r.URL.Query()
		if r.Method == http.MethodPost {
			// OAuth authorization and the user's decision must come from
			// the submitted form, never from query parameters merged into it.
			values = r.PostForm
		}
		params := parseAuthorizeParams(values)

		// Validate before anything else, including the login bounce, so a
		// malformed request is reported rather than surviving a round trip
		// through the login page.
		req, err := core.PrepareOAuthAuthorization.Call(r.Context(), p.app, params)
		if err != nil {
			p.writeAuthorizeError(w, r, params, err)
			return
		}

		user := userFromContext(r.Context())
		if user == nil {
			returnTo := "/oauth/authorize?" + values.Encode()
			http.Redirect(w, r, "/login?return_to="+url.QueryEscape(returnTo), http.StatusSeeOther)
			return
		}

		decision := r.PostForm["approve"]
		approved := r.Method == http.MethodPost && len(decision) == 1 && decision[0] == "true"
		denied := r.Method == http.MethodPost && len(decision) == 1 && decision[0] == "false"

		if denied {
			p.redirectAuthorizeError(w, r, params, "access_denied", "user denied the request")
			return
		}
		if !approved {
			// Only the clicked button may supply the decision. Reflecting an
			// incoming approve field would let it override a later Deny click.
			delete(values, "approve")
			renderConsentPage(w, r, consentData{
				Username:    user.Username,
				ClientID:    req.Client.ID,
				ClientName:  req.Client.ClientName,
				RedirectURI: params.RedirectURI,
				Scopes:      strings.Fields(req.Scope),
				FormFields:  values,
			})
			return
		}

		result, err := core.CreateOAuthAuthorizationCode.Call(core.WithUserID(r.Context(), user.ID), p.app, params)
		if err != nil {
			p.writeAuthorizeError(w, r, params, err)
			return
		}
		p.redirectAuthorizeSuccess(w, r, params, result.Code)
	}
}

func (provider *oauthProvider) redirectAuthorizeSuccess(w http.ResponseWriter, r *http.Request, p core.OAuthAuthorizationParams, code string) {
	u, _ := url.Parse(p.RedirectURI)
	q := u.Query()
	q.Set("code", code)
	q.Set("state", p.State)
	q.Set("iss", provider.canonicalURL)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// writeAuthorizeError reports an authorization failure. A failure found
// before the redirect URI was confirmed to belong to the client cannot be
// redirected anywhere, so it is rendered inline; everything else goes back to
// the client per OAuth 2.1.
func (provider *oauthProvider) writeAuthorizeError(w http.ResponseWriter, r *http.Request, p core.OAuthAuthorizationParams, err error) {
	var oauthErr *core.OAuthError
	if !errors.As(err, &oauthErr) {
		internalError(w, r, err)
		return
	}
	if !oauthErr.Redirectable {
		httplog.SetError(r.Context(), oauthErr)
		writeOAuthError(w, http.StatusBadRequest, oauthErr.Code, oauthErr.Description)
		return
	}
	provider.redirectAuthorizeError(w, r, p, oauthErr.Code, oauthErr.Description)
}

func (provider *oauthProvider) redirectAuthorizeError(w http.ResponseWriter, r *http.Request, p core.OAuthAuthorizationParams, code, desc string) {
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
	q.Set("iss", provider.canonicalURL)
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
			tokens, err := core.ExchangeOAuthCode.Call(r.Context(), p.app, core.ExchangeOAuthCodeParams{
				ClientID:     r.Form.Get("client_id"),
				Code:         r.Form.Get("code"),
				RedirectURI:  r.Form.Get("redirect_uri"),
				CodeVerifier: r.Form.Get("code_verifier"),
				Resource:     r.Form.Get("resource"),
			})
			if err != nil {
				writeOAuthActionError(w, r, err)
				return
			}
			writeTokenResponse(w, tokens)
		case "refresh_token":
			tokens, err := core.RefreshOAuthToken.Call(r.Context(), p.app, core.RefreshOAuthTokenParams{
				ClientID:     r.Form.Get("client_id"),
				RefreshToken: r.Form.Get("refresh_token"),
				Resource:     r.Form.Get("resource"),
			})
			if err != nil {
				writeOAuthActionError(w, r, err)
				return
			}
			writeTokenResponse(w, tokens)
		default:
			writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		}
	}
}

func writeTokenResponse(w http.ResponseWriter, t core.OAuthTokens) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  t.AccessToken,
		"refresh_token": t.RefreshToken,
		"token_type":    "Bearer",
		"expires_in":    int(core.OAuthAccessTokenLifespan.Seconds()),
		"scope":         t.Scope,
	})
}

// ===== Revocation endpoint (RFC 7009) =====

func (p *oauthProvider) handleRevoke() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "could not parse form body")
			return
		}
		// Per RFC 7009 §2.2 the response is 200 even for an unknown token, so
		// a storage failure is logged rather than reported to the client.
		if _, err := core.RevokeOAuthToken.Call(r.Context(), p.app, core.RevokeOAuthTokenParams{
			Token:         r.Form.Get("token"),
			TokenTypeHint: r.Form.Get("token_type_hint"),
		}); err != nil {
			httplog.SetError(r.Context(), err)
		}
		w.WriteHeader(http.StatusOK)
	}
}

// ===== Bearer verification (used by MCP middleware) =====

func (p *oauthProvider) verifyAccessToken(ctx context.Context, token string) (*AuthUser, error) {
	user, err := core.AuthenticateOAuthToken.Call(ctx, p.app, core.AuthenticateOAuthTokenParams{Token: token})
	switch {
	case errors.Is(err, core.ErrOAuthInvalidToken), errors.Is(err, core.ErrOAuthTokenAudienceMismatch), errors.Is(err, core.ErrOAuthInsufficientScope):
		return nil, err
	case err != nil:
		// An unexpected failure must not describe itself in the bearer
		// challenge, so log it and answer like any other bad token.
		httplog.SetError(ctx, err)
		return nil, core.ErrOAuthInvalidToken
	}
	return &user, nil
}

// ===== Helpers =====

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{
		"error":             code,
		"error_description": desc,
	})
}

// writeOAuthActionError translates a core error into an OAuth error response.
// Expected protocol failures carry their own code and a description that is
// safe to return verbatim; anything else is an internal error.
func writeOAuthActionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, core.ErrOAuthClientLimit) {
		w.Header().Set("Retry-After", "3600")
		writeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "client registration capacity reached; retry later")
		return
	}
	var oauthErr *core.OAuthError
	if !errors.As(err, &oauthErr) {
		internalError(w, r, err)
		return
	}
	if errors.Is(err, core.ErrOAuthRefreshReuse) {
		// Record the detection internally; the client is told only that the
		// grant is invalid so it cannot tell reuse from any other failure.
		httplog.SetError(r.Context(), err)
	}
	writeOAuthError(w, http.StatusBadRequest, oauthErr.Code, oauthErr.Description)
}
