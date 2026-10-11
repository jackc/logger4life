package server

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
)

// decodeJSONObject validates an application request before invoking its action.
// Protocol handlers can explicitly allow unknown metadata members.
func decodeJSONObject(reader io.Reader, dest any, options ...json.Options) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if trimmed := bytes.TrimLeft(data, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("expected JSON object")
	}
	return json.Unmarshal(data, dest, json.RejectUnknownMembers(true), json.JoinOptions(options...))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	// Encode before committing headers: v2 can reject invalid Unicode in output.
	data, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"error":"internal error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
