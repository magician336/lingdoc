package types

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// A stored credential that cannot be decrypted must not look like a model
// that has no credential.
//
// "No credential configured" is a normal state: the operator has not entered
// a key yet. "A credential exists but SYSTEM_AES_KEY cannot decrypt it" is a
// misconfiguration that has to be reported — before the old behaviour it was
// flattened into the first one, so the problem only showed up when a call to
// the provider was made.
//
// These tests use a self-made ciphertext: no real key and no real secret ever
// enters the repository.
// ---------------------------------------------------------------------------

// credentialTestKey is a self-made 32-byte key (the length AES-256 needs).
const credentialTestKey = "0123456789abcdef0123456789abcdef"

// credentialTestOtherKey is a second self-made key, used to produce
// ciphertext that cannot be decrypted with credentialTestKey.
const credentialTestOtherKey = "abcdef0123456789abcdef0123456789"

// captureLog records what the standard logger writes while fn runs.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buffer bytes.Buffer
	original := log.Writer()
	flags := log.Flags()
	log.SetOutput(&buffer)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(original)
		log.SetFlags(flags)
	})
	fn()
	return buffer.String()
}

func TestModelParametersScanReportsUndecryptableCredential(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", credentialTestKey)
	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(credentialTestOtherKey))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ciphertext, utils.EncPrefix))

	params := ModelParameters{}
	var scanErr error
	logged := captureLog(t, func() {
		scanErr = params.Scan([]byte(`{"api_key":"` + ciphertext + `"}`))
	})
	require.NoError(t, scanErr, "one broken row must still load")

	// The unusable value never travels upstream.
	assert.Empty(t, params.APIKey, "an undecryptable credential must not be handed out as a key")

	// The failure is explicit and observable, not a silent empty value.
	require.Error(t, params.CredentialDecryptError())
	assert.ErrorIs(t, params.CredentialDecryptError(), ErrCredentialDecryptFailed)
	assert.Contains(t, params.CredentialDecryptError().Error(), "api_key",
		"the error must name the field that could not be decrypted")

	// The log says what happened, and says it is NOT an unconfigured credential.
	assert.Contains(t, logged, "decrypt failed")
	assert.Contains(t, logged, "NOT an unconfigured credential")
}

func TestModelParametersCredentialDecryptErrorCarriesNoSecret(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", credentialTestKey)
	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(credentialTestOtherKey))
	require.NoError(t, err)

	params := ModelParameters{}
	logged := captureLog(t, func() {
		require.NoError(t, params.Scan([]byte(`{"api_key":"`+ciphertext+`"}`)))
	})
	credentialErr := params.CredentialDecryptError()
	require.Error(t, credentialErr)

	for _, forbidden := range []string{
		ciphertext,
		"sk-self-made-secret",
		credentialTestKey,
		credentialTestOtherKey,
	} {
		assert.NotContains(t, credentialErr.Error(), forbidden,
			"the error must not carry the ciphertext or any key fragment")
		assert.NotContains(t, logged, forbidden,
			"the log must not carry the ciphertext or any key fragment")
	}
}

func TestModelParametersWithoutCredentialIsUnconfiguredNotFailed(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", credentialTestKey)

	params := ModelParameters{}
	logged := captureLog(t, func() {
		require.NoError(t, params.Scan([]byte(`{"api_key":"","base_url":"https://models.example.com/v1"}`)))
	})

	assert.Empty(t, params.APIKey)
	assert.NoError(t, params.CredentialDecryptError(),
		"a model without a credential is unconfigured, not a decrypt failure")
	assert.NotContains(t, logged, "decrypt failed",
		"an unconfigured model must not be reported as a decrypt failure")
}

func TestModelParametersDecryptsCredentialSuccessfully(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", credentialTestKey)
	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(credentialTestKey))
	require.NoError(t, err)

	params := ModelParameters{}
	logged := captureLog(t, func() {
		require.NoError(t, params.Scan([]byte(`{"api_key":"`+ciphertext+`"}`)))
	})

	assert.Equal(t, "sk-self-made-secret", params.APIKey)
	assert.NoError(t, params.CredentialDecryptError())
	assert.NotContains(t, logged, "decrypt failed")
}

func TestModelParametersDecryptsLegacyPlaintextCredential(t *testing.T) {
	// A row written before encryption was enabled has no enc:v1: prefix. It
	// is a configured credential, not a decrypt failure.
	t.Setenv("SYSTEM_AES_KEY", credentialTestKey)

	params := ModelParameters{}
	require.NoError(t, params.Scan([]byte(`{"api_key":"sk-legacy-plaintext"}`)))

	assert.Equal(t, "sk-legacy-plaintext", params.APIKey)
	assert.NoError(t, params.CredentialDecryptError())
}

func TestModelParametersReportsAppSecretDecryptFailure(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", credentialTestKey)
	ciphertext, err := utils.EncryptAESGCM("app-secret-self-made", []byte(credentialTestOtherKey))
	require.NoError(t, err)

	params := ModelParameters{}
	require.NoError(t, params.Scan([]byte(`{"app_secret":"`+ciphertext+`"}`)))

	assert.Empty(t, params.AppSecret)
	require.Error(t, params.CredentialDecryptError())
	assert.Contains(t, params.CredentialDecryptError().Error(), "app_secret")
}

func TestCredentialDecryptFailureIsNotPersisted(t *testing.T) {
	// The failure describes one load, not the row: it must not end up in the
	// parameters column, and a re-loaded row must not claim a failure that
	// happened in an earlier process.
	t.Setenv("SYSTEM_AES_KEY", credentialTestKey)
	ciphertext, err := utils.EncryptAESGCM("sk-self-made-secret", []byte(credentialTestOtherKey))
	require.NoError(t, err)

	params := ModelParameters{}
	require.NoError(t, params.Scan([]byte(`{"api_key":"`+ciphertext+`"}`)))
	require.Error(t, params.CredentialDecryptError())

	stored, err := params.Value()
	require.NoError(t, err)
	encoded, ok := stored.([]byte)
	require.True(t, ok)
	assert.NotContains(t, string(encoded), "credentialDecryptFailure")
	assert.NotContains(t, string(encoded), "decrypt failed")

	var reloaded ModelParameters
	require.NoError(t, reloaded.Scan(encoded))
	assert.NoError(t, reloaded.CredentialDecryptError(),
		"a persisted row carries no failure state of its own")
}

func TestCredentialDecryptErrorIsEmptyForAFreshParameters(t *testing.T) {
	params := ModelParameters{}
	assert.NoError(t, params.CredentialDecryptError())

	// A parameters blob that is not valid JSON must not be mistaken for a
	// decrypt failure either.
	broken := ModelParameters{}
	assert.Error(t, broken.Scan([]byte("not json")))
	assert.NoError(t, broken.CredentialDecryptError())
}

func TestModelParametersValueDoesNotLeakTheFailureState(t *testing.T) {
	// The parameters column stays a plain credential blob: the guard state is
	// not part of the persisted shape.
	params := ModelParameters{BaseURL: "https://models.example.com/v1"}
	stored, err := params.Value()
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stored.([]byte), &decoded))
	assert.NotContains(t, decoded, "credentialDecryptFailure")
}
