package backend

import (
	"html/template"
	"net/http"
	"net/url"
)

// consentData drives the OAuth consent page rendered at GET /oauth/authorize
// once the user is signed in. The form posts back to /oauth/authorize with
// every original query param plus an `approve` field.
type consentData struct {
	Username    string
	ClientID    string
	RedirectURI string
	Scopes      []string
	FormFields  url.Values
}

var consentTemplate = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Authorize MCP access</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; max-width: 32rem; margin: 4rem auto; padding: 0 1rem; line-height: 1.5; color: #1a1a1a; }
  h1 { font-size: 1.25rem; margin-bottom: 1rem; }
  .card { border: 1px solid #ddd; border-radius: 8px; padding: 1.5rem; }
  .meta { color: #666; font-size: 0.9rem; margin: 0.25rem 0; }
  ul { padding-left: 1.25rem; }
  .actions { margin-top: 1.5rem; display: flex; gap: 0.75rem; }
  button { font-size: 1rem; padding: 0.5rem 1.25rem; border-radius: 6px; cursor: pointer; border: 1px solid #ccc; background: #fff; }
  button.primary { background: #2563eb; color: white; border-color: #2563eb; }
  button.primary:hover { background: #1d4ed8; }
</style>
</head>
<body>
  <div class="card">
    <h1>Authorize MCP access</h1>
    <p>Signed in as <strong>{{.Username}}</strong>.</p>
    <p>Application <code>{{.ClientID}}</code> wants to access your Logger4Life data.</p>
    {{if .RedirectURI}}<p class="meta">Will redirect to: <code>{{.RedirectURI}}</code></p>{{end}}
    {{if .Scopes}}
    <p>Requested scopes:</p>
    <ul>{{range .Scopes}}<li><code>{{.}}</code></li>{{end}}</ul>
    {{end}}

    <form method="POST" action="/oauth/authorize">
      {{range $key, $values := .FormFields}}{{range $values}}<input type="hidden" name="{{$key}}" value="{{.}}">
      {{end}}{{end}}
      <div class="actions">
        <button type="submit" name="approve" value="true" class="primary">Approve</button>
        <button type="submit" name="approve" value="false">Deny</button>
      </div>
    </form>
  </div>
</body>
</html>`))

func renderConsentPage(w http.ResponseWriter, _ *http.Request, data consentData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// We rely on SameSite=Lax cookies + the form posting same-origin to
	// prevent CSRF: a cross-origin attacker cannot read the consent page or
	// the user's session cookie, so they cannot construct a valid POST.
	if err := consentTemplate.Execute(w, data); err != nil {
		http.Error(w, "render consent page", http.StatusInternalServerError)
	}
}
