package service

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The chat path refuses a model whose stored credential cannot be decrypted.
//
// The row knows the difference between "no credential was configured" and
// "a credential exists and this process cannot read it". Before this guard,
// the second state reached the provider as an empty API key and the provider
// refused the call, far from the cause.
//
// The test uses a self-made ciphertext and a self-made key: no real key and
// no real secret enters the repository, and nothing is printed.
// ---------------------------------------------------------------------------

const (
	credentialGuardKey      = "0123456789abcdef0123456789abcdef"
	credentialGuardOtherKey = "abcdef0123456789abcdef0123456789"
)

func TestGetChatModelRefusesAModelWhoseCredentialCannotBeDecrypted(t *testing.T) {
	allowLoopbackForTest(t)
	t.Setenv("SYSTEM_AES_KEY", credentialGuardKey)
	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(credentialGuardOtherKey))
	require.NoError(t, err)

	model := &types.Model{
		ID: "model-1", Name: "qwen3.8-flash",
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
	}
	// The repository loads the row through the scanner, so the parameters are
	// decrypted exactly as they are in production.
	require.NoError(t, model.Parameters.Scan([]byte(
		`{"base_url":"http://127.0.0.1:1/v1","provider":"aliyun","api_key":"`+ciphertext+`"}`)))
	require.Error(t, model.Parameters.CredentialDecryptError())

	svc := newChatTypeGuardService(model)
	client, err := svc.GetChatModel(ctxWithTenant(1), "model-1")

	require.Error(t, err, "a credential nobody can read must not produce a client")
	require.ErrorIs(t, err, types.ErrCredentialDecryptFailed)
	assert.Nil(t, client, "no client may be built for an unusable credential")
}

// A deployment without a usable SYSTEM_AES_KEY stores the credential as
// PLAIN TEXT (utils.GetAESKey only accepts a key of exactly 32 bytes, so the
// writer encrypts nothing). That is a CONFIGURED credential: it must load,
// it must not raise the decrypt failure, and the chat path must keep working
// with it.
//
// This is the shape the real environment is in today, so the guard must not
// break it.
func TestGetChatModelKeepsAPlaintextCredentialUsable(t *testing.T) {
	allowLoopbackForTest(t)
	t.Setenv("SYSTEM_AES_KEY", "")

	model := &types.Model{
		ID: "model-1", Name: "qwen3.8-flash",
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
	}
	require.NoError(t, model.Parameters.Scan([]byte(
		`{"base_url":"http://127.0.0.1:1/v1","provider":"aliyun","api_key":"sk-self-made-plaintext"}`)))
	require.NoError(t, model.Parameters.CredentialDecryptError(),
		"a plaintext credential is configured, not a decrypt failure")
	require.NotEmpty(t, model.Parameters.APIKey)

	svc := newChatTypeGuardService(model)
	client, err := svc.GetChatModel(ctxWithTenant(1), "model-1")

	require.NoError(t, err, "a plaintext credential must still produce a client")
	assert.NotNil(t, client)
}

// A deployment without a usable SYSTEM_AES_KEY cannot read a row that WAS
// written encrypted. The value is blanked and the failure is explicit, so the
// chat path refuses instead of sending an empty key upstream.
func TestGetChatModelRefusesAnEncryptedCredentialWithoutAUsableKey(t *testing.T) {
	allowLoopbackForTest(t)
	t.Setenv("SYSTEM_AES_KEY", credentialGuardKey)
	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(credentialGuardKey))
	require.NoError(t, err)

	model := &types.Model{
		ID: "model-1", Name: "qwen3.8-flash",
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
	}
	// The key goes away between the write and the load: the row was written
	// encrypted, and this process can no longer read it.
	t.Setenv("SYSTEM_AES_KEY", "")
	require.NoError(t, model.Parameters.Scan([]byte(
		`{"base_url":"http://127.0.0.1:1/v1","provider":"aliyun","api_key":"`+ciphertext+`"}`)))
	require.Error(t, model.Parameters.CredentialDecryptError())
	require.Empty(t, model.Parameters.APIKey, "an unreadable credential must be blanked")

	svc := newChatTypeGuardService(model)
	client, err := svc.GetChatModel(ctxWithTenant(1), "model-1")

	require.Error(t, err)
	require.ErrorIs(t, err, types.ErrCredentialDecryptFailed)
	assert.Nil(t, client)
}

func TestGetChatModelKeepsAReadableCredentialUsable(t *testing.T) {
	allowLoopbackForTest(t)
	t.Setenv("SYSTEM_AES_KEY", credentialGuardKey)
	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(credentialGuardKey))
	require.NoError(t, err)

	model := &types.Model{
		ID: "model-1", Name: "qwen3.8-flash",
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
	}
	require.NoError(t, model.Parameters.Scan([]byte(
		`{"base_url":"http://127.0.0.1:1/v1","provider":"aliyun","api_key":"`+ciphertext+`"}`)))
	require.NoError(t, model.Parameters.CredentialDecryptError())
	require.NotEmpty(t, model.Parameters.APIKey, "the decrypted credential must not be empty")

	svc := newChatTypeGuardService(model)
	client, err := svc.GetChatModel(ctxWithTenant(1), "model-1")

	require.NoError(t, err, "a readable credential must still produce a client")
	assert.NotNil(t, client)
}

// The stored parameters must never carry the failure state of one load: the
// guard reads a property of the loaded row, not of the persisted blob.
func TestCredentialGuardDoesNotPersistTheFailureState(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", credentialGuardKey)
	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(credentialGuardOtherKey))
	require.NoError(t, err)

	var parameters types.ModelParameters
	require.NoError(t, parameters.Scan([]byte(`{"api_key":"`+ciphertext+`"}`)))
	require.Error(t, parameters.CredentialDecryptError())

	stored, err := parameters.Value()
	require.NoError(t, err)
	encoded, ok := stored.([]byte)
	require.True(t, ok)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.NotContains(t, decoded, "credentialDecryptFailure")
}
