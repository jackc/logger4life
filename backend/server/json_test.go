package server

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/manifest/httpregistry"
	"github.com/stretchr/testify/require"
)

func TestApplicationHandlersRejectInvalidJSON(t *testing.T) {
	// Invalid input must fail before any handler needs application dependencies.
	for name, handler := range map[string]http.HandlerFunc{
		"login": handleLogin(nil), "register": handleRegister(nil, true),
		"create-log": handleCreateLog(nil), "create-folder": handleCreateFolder(nil),
		"sql": handleExecuteSQL(nil), "passkey": handlePasskeyLoginFinish(nil),
	} {
		t.Run(name, func(t *testing.T) {
			for _, input := range []string{"null", "[]", "", `{} {}`, `{"unexpected":true}`, `{"unexpected":1,"unexpected":2}`} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(input)))
				require.Equal(t, http.StatusBadRequest, w.Code, input)
				require.JSONEq(t, `{"error":"invalid request body"}`, w.Body.String())
			}
		})
	}
}

// Handlers decode with httpregistry.DecodeJSONBody; these are the
// application's own reliances on it.
func TestDecodeJSONBodyPreservesProtocolExtensions(t *testing.T) {
	decode := func(body string, dest any, maxBytes int64, options ...json.Options) error {
		return httpregistry.DecodeJSONBody(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body)), dest, maxBytes, options...)
	}
	var params core.FinishPasskeyLoginParams
	input := `{"challenge_id":"challenge","credential":{"id":"credential","clientExtensionResults":{"future":true}}}`
	require.NoError(t, decode(input, &params, 0))
	require.JSONEq(t, `{"id":"credential","clientExtensionResults":{"future":true}}`, string(params.Credential))
	// Opaque credentials still undergo JSON syntax and duplicate-name checks.
	require.Error(t, decode(`{"credential":{"id":"a","id":"b"}}`, &params, 0))

	var metadata core.RegisterOAuthClientParams
	require.NoError(t, decode(`{"client_name":"client","future_metadata":true}`, &metadata, 0, json.RejectUnknownMembers(false)))
	require.Equal(t, "client", metadata.ClientName)
	require.Error(t, decode(`{"client_name":"a","client_name":"b"}`, &metadata, 0, json.RejectUnknownMembers(false)))

	err := decode("{}"+strings.Repeat(" ", 32), &metadata, 16)
	var tooLarge *http.MaxBytesError
	require.ErrorAs(t, err, &tooLarge)
}

func TestWriteJSONUsesV2ResponseSemantics(t *testing.T) {
	emptyEmail := ""
	for _, tc := range []struct {
		name   string
		value  any
		status int
		want   string
	}{
		{"nil-list", []core.Log(nil), 200, `[]`},
		{"nil-map", map[string]any(nil), 200, `{}`},
		{"nil-email", core.User{ID: "id", Username: "alice"}, 200, `{"id":"id","username":"alice"}`},
		{"empty-email", core.User{ID: "id", Username: "alice", Email: &emptyEmail}, 200, `{"id":"id","username":"alice","email":""}`},
		{"invalid-unicode", "\xff", 500, `{"error":"internal error"}`},
		{"invalid-raw-json", jsontext.Value(`{`), 500, `{"error":"internal error"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeJSON(w, http.StatusOK, tc.value)
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, "application/json", w.Header().Get("Content-Type"))
			require.JSONEq(t, tc.want, w.Body.String())
		})
	}
}
