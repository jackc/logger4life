package cimd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testID = "https://example.com/client.json"

func metadataDocument() map[string]any {
	return map[string]any{"client_id": testID, "client_name": "Example Client",
		"redirect_uris":              []string{"https://client.example/callback", "http://[::1]:54321/cb"},
		"token_endpoint_auth_method": "none"}
}

func documentJSON(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(doc)
	require.NoError(t, err)
	return body
}

func TestMetadataDocuments(t *testing.T) {
	client, err := parseMetadata(testID, documentJSON(t, metadataDocument()))
	require.NoError(t, err)
	require.Equal(t, testID, client.ID)
	require.True(t, client.AuthorizationCodeOnly, "RFC 7591 defaults to authorization_code")
	doc := metadataDocument()
	doc["grant_types"] = []string{"authorization_code", "refresh_token"}
	doc["response_types"] = []string{"code"}
	doc["application_type"] = "native"
	doc["scope"] = "mcp"
	doc["logo_uri"] = "https://127.0.0.1/private"
	client, err = parseMetadata(testID, documentJSON(t, doc))
	require.NoError(t, err, "unused extension URLs are never dereferenced")
	require.False(t, client.AuthorizationCodeOnly)

	for _, tc := range []struct {
		key   string
		value any
	}{
		{"client_id", nil}, {"client_id", "https://EXAMPLE.com/client.json"},
		{"client_id", "https://example.com:443/client.json"}, {"client_id", "https://example.com/Client.json"},
		{"client_name", nil}, {"client_name", "  "}, {"client_name", 3}, {"client_name", strings.Repeat("a", 257)},
		{"redirect_uris", nil}, {"redirect_uris", []string{}}, {"redirect_uris", "https://example.com/cb"},
		{"redirect_uris", []string{"https:///cb"}}, {"redirect_uris", []string{"https://a.example/cb#"}},
		{"redirect_uris", []string{"https://user@a.example/cb"}}, {"redirect_uris", []string{"http://a.example/cb"}},
		{"redirect_uris", make([]string, 11)}, {"redirect_uris", []string{"https://a.example/" + strings.Repeat("a", 2048)}},
		{"token_endpoint_auth_method", nil}, {"token_endpoint_auth_method", "private_key_jwt"},
		{"token_endpoint_auth_method", "client_secret_basic"}, {"token_endpoint_auth_method", ""},
		{"client_secret", nil}, {"client_secret_expires_at", 0}, {"jwks", map[string]any{"keys": []any{}}},
		{"grant_types", nil}, {"grant_types", []string{"refresh_token"}}, {"grant_types", []string{"authorization_code", "client_credentials"}},
		{"response_types", nil}, {"response_types", []string{"token"}},
		{"scope", nil}, {"scope", "other"}, {"scope", "mcp other"},
	} {
		t.Run(tc.key+"/"+string(documentJSON(t, map[string]any{"v": tc.value})), func(t *testing.T) {
			doc := metadataDocument()
			doc[tc.key] = tc.value
			_, err := parseMetadata(testID, documentJSON(t, doc))
			require.Error(t, err)
		})
	}
	for _, key := range []string{"client_id", "client_name", "redirect_uris", "token_endpoint_auth_method"} {
		doc := metadataDocument()
		delete(doc, key)
		_, err := parseMetadata(testID, documentJSON(t, doc))
		require.Error(t, err, "missing %s", key)
	}
	valid := string(documentJSON(t, metadataDocument()))
	for _, body := range []string{"null", "[]", "{}", valid + "{}", valid + "junk", valid[:len(valid)-1],
		`{"client_id":"different",` + valid[1:], strings.Replace(valid, "client_id", "CLIENT_ID", 1),
		strings.Replace(valid, "Example Client", string([]byte{0xff}), 1)} {
		_, err := parseMetadata(testID, []byte(body))
		require.Error(t, err, "%q", body)
	}
}
