package jedstore

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/jackc/logger4life/backend/domain"
	"github.com/stretchr/testify/require"
)

func TestJSONScannerReadsV1StoredValues(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	ctx := context.Background()
	var fields []domain.FieldDefinition
	require.NoError(t, store.conn(ctx).QueryRow(ctx, `SELECT $1::jsonb`, `[{"name":"\u003cdose\u003e","type":"number","required":false}]`).Scan(&fields))
	require.Equal(t, []domain.FieldDefinition{{Name: "<dose>", Type: "number"}}, fields)
	var values map[string]any
	require.NoError(t, store.conn(ctx).QueryRow(ctx, `SELECT $1::jsonb`, `{"dose":"500","optional":null,"taken":true}`).Scan(&values))
	require.Equal(t, map[string]any{"dose": "500", "optional": nil, "taken": true}, values)
	var redirects []string
	require.NoError(t, store.conn(ctx).QueryRow(ctx, `SELECT $1::jsonb`, `["https://example.com/cb?a=1\u0026b=2"]`).Scan(&redirects))
	require.Equal(t, []string{"https://example.com/cb?a=1&b=2"}, redirects)
	require.NoError(t, store.conn(ctx).QueryRow(ctx, `SELECT $1::jsonb`, `null`).Scan(&fields))
	require.Nil(t, fields)
	var raw jsontext.Value
	require.NoError(t, store.conn(ctx).QueryRow(ctx, `SELECT $1::jsonb`, jsontext.Value(`{"old":true}`)).Scan(&raw))
	require.JSONEq(t, `{"old":true}`, string(raw))
}
