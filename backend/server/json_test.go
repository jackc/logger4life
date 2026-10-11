package server

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
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

func TestDecodeJSONObjectPreservesProtocolExtensions(t *testing.T) {
	var params core.FinishPasskeyLoginParams
	input := `{"challenge_id":"challenge","credential":{"id":"credential","clientExtensionResults":{"future":true}}}`
	require.NoError(t, decodeJSONObject(strings.NewReader(input), &params))
	require.JSONEq(t, `{"id":"credential","clientExtensionResults":{"future":true}}`, string(params.Credential))
	// Opaque credentials still undergo JSON syntax and duplicate-name checks.
	require.Error(t, decodeJSONObject(strings.NewReader(`{"credential":{"id":"a","id":"b"}}`), &params))

	var metadata core.RegisterOAuthClientParams
	require.NoError(t, decodeJSONObject(strings.NewReader(`{"client_name":"client","future_metadata":true}`), &metadata, json.RejectUnknownMembers(false)))
	require.Equal(t, "client", metadata.ClientName)
	require.Error(t, decodeJSONObject(strings.NewReader(`{"client_name":"a","client_name":"b"}`), &metadata, json.RejectUnknownMembers(false)))

	reader := http.MaxBytesReader(httptest.NewRecorder(), io.NopCloser(strings.NewReader("{}"+strings.Repeat(" ", 32))), 16)
	err := decodeJSONObject(reader, &metadata)
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
