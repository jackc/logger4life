package server

import (
	"encoding/json/v2"
	"net/http"
)

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
