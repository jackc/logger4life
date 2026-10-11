package core

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/require"
)

func TestPasskeyChallengeReadsV1Session(t *testing.T) {
	// Captured with encoding/json before the v2 migration, including base64
	// byte slices, camel-case library tags, and an omitted false boolean.
	const stored = `{"challenge":"old-challenge","rpId":"localhost","origin":"http://localhost","user_id":"AQID","allowed_credentials":["BAUG"],"expires":"2099-01-01T00:00:00Z","userVerification":"required"}`
	expires := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	challenges := &fakePasskeyChallengeStore{challenges: map[string]PasskeyChallenge{
		"old": {ID: "old", SessionData: []byte(stored), Kind: PasskeyLoginChallenge, ExpiresAt: expires},
	}}
	app := New(Config{Challenges: challenges})
	session, err := consumePasskeyChallenge(context.Background(), app, "old", PasskeyLoginChallenge, nil)
	require.NoError(t, err)
	want := webauthn.SessionData{
		Challenge: "old-challenge", RelyingPartyID: "localhost", Origin: "http://localhost",
		UserID: []byte{1, 2, 3}, AllowedCredentialIDs: [][]byte{{4, 5, 6}},
		Expires: expires, UserVerification: protocol.VerificationRequired,
	}
	require.Equal(t, want, session)
	id, err := storePasskeyChallenge(context.Background(), app, nil, &session, PasskeyLoginChallenge)
	require.NoError(t, err)
	roundTrip, err := consumePasskeyChallenge(context.Background(), app, id, PasskeyLoginChallenge, nil)
	require.NoError(t, err)
	require.Equal(t, want, roundTrip)
}

func TestCollectionCursorReadsV1JSON(t *testing.T) {
	const stored = `{"v":1,"collection":"logs","user":"alice","name":"\u003cwater\u003e","id":"log-id"}`
	value := base64.RawURLEncoding.EncodeToString([]byte(stored))
	cursor, err := decodeCollectionCursor(value, "logs", "alice")
	require.NoError(t, err)
	require.Equal(t, "<water>", cursor.Name)
	require.Equal(t, "log-id", cursor.ID)
	_, err = decodeCollectionCursor(value, "logs", "bob")
	require.Error(t, err)
}

func TestJSONOptionalScalarFieldsRemainOmitted(t *testing.T) {
	data, err := json.Marshal(CollectionPageParams{})
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(data))
	data, err = json.Marshal(OAuthClient{ID: "client", RedirectURIs: []string{}})
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"client","redirect_uris":[]}`, string(data))
}
