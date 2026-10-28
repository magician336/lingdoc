package types

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The shared JSON-column reader.
//
// A JSON column reaches the scanner in one of two shapes, and which one
// depends on the dialect and on the storage class of the value that was
// written — not on the column. See json_column_dialect_test.go for the
// measured driver behaviour; this file pins the contract those measurements
// imply.
// ---------------------------------------------------------------------------

func TestJSONColumnBytesAcceptsBothDriverCarriers(t *testing.T) {
	raw, err := jsonColumnBytes([]byte(`{"provider":"aliyun"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"provider":"aliyun"}`, string(raw))

	raw, err = jsonColumnBytes(`{"provider":"aliyun"}`)
	require.NoError(t, err)
	assert.Equal(t, `{"provider":"aliyun"}`, string(raw))
}

func TestJSONColumnBytesRejectsUnknownCarrier(t *testing.T) {
	// An unrecognised carrier must be reported, not dropped: a dropped value
	// leaves the caller with a configuration that looks "not configured".
	for _, value := range []any{42, int64(42), 3.5, true, struct{}{}, []string{"{}"}} {
		_, err := jsonColumnBytes(value)
		require.Error(t, err, "carrier %T must not be accepted silently", value)
		assert.ErrorIs(t, err, ErrUnsupportedJSONColumnValue)
	}
}

func TestScanJSONColumnReadsBothCarriers(t *testing.T) {
	for _, value := range []any{[]byte(`{"provider":"aliyun"}`), `{"provider":"aliyun"}`} {
		var params ModelParameters
		require.NoError(t, scanJSONColumn(value, &params))
		assert.Equal(t, "aliyun", params.Provider)
	}
}

func TestScanJSONColumnLeavesReceiverOnNull(t *testing.T) {
	params := ModelParameters{Provider: "kept"}
	require.NoError(t, scanJSONColumn(nil, &params))
	assert.Equal(t, "kept", params.Provider, "a NULL column must not clear the receiver")
}

func TestScanJSONColumnRejectsUnknownCarrier(t *testing.T) {
	var params ModelParameters
	err := scanJSONColumn(42, &params)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedJSONColumnValue)
}

// ---------------------------------------------------------------------------
// #609 — the model row that could never be approved.
//
// The `models.parameters` column is declared TEXT, but this application binds
// the []byte that json.Marshal returns, so SQLite stores an application-written
// row as BLOB. A row written by anything else — a literal SQL INSERT in a
// migration, an ops script, an import — is stored as TEXT and reaches the
// scanner as a string. The scanner used to accept []byte only and returned
// without a word for anything else, so such a row loaded with an empty
// configuration: no provider, no endpoint, no credential. It was listed as a
// configured model and could never be approved.
// ---------------------------------------------------------------------------

const modelParamsTestAESKey = "0123456789abcdef0123456789abcdef"

func TestModelParametersScanReadsTextCarrier(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", modelParamsTestAESKey)
	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(modelParamsTestAESKey))
	require.NoError(t, err)

	params := ModelParameters{}
	require.NoError(t, params.Scan(`{"provider":"aliyun","base_url":"https://models.example.com/v1","api_key":"`+ciphertext+`"}`))

	assert.Equal(t, "aliyun", params.Provider)
	assert.Equal(t, "https://models.example.com/v1", params.BaseURL)
	assert.Equal(t, "sk-self-made-secret", params.APIKey)
	assert.NoError(t, params.CredentialDecryptError())
}

func TestModelParametersScanRejectsUnknownCarrier(t *testing.T) {
	params := ModelParameters{}
	err := params.Scan(42)
	require.Error(t, err, "an unknown carrier must be reported, not turned into an empty configuration")
	assert.ErrorIs(t, err, ErrUnsupportedJSONColumnValue)
}

// Every JSON column shares the reader, so the same carrier that used to be
// dropped silently is now readable everywhere — and reported everywhere when
// it is not a carrier at all.
func TestSharedReaderCoversTheOtherJSONColumns(t *testing.T) {
	var config AutoTagConfig
	require.NoError(t, config.Scan(`{"enabled":true}`))
	assert.True(t, config.Enabled)

	var references References
	require.NoError(t, references.Scan(`[{"id":"doc-1"}]`))
	require.Len(t, references, 1)
	assert.Equal(t, "doc-1", references[0].ID)

	for _, scan := range []func(any) error{
		func(v any) error { return config.Scan(v) },
		func(v any) error { return references.Scan(v) },
		func(v any) error { return (&ChunkingConfig{}).Scan(v) },
		func(v any) error { return (&WikiConfig{}).Scan(v) },
		func(v any) error { return (&MCPAuthConfig{}).Scan(v) },
	} {
		err := scan(42)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnsupportedJSONColumnValue)
	}
}
