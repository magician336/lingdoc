package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A provider that sends its own request identifier leaves a link to its own
// records; one that sends none must not leave a made-up one.
func TestRemoteAPIChatRecordsTheProviderRequestIdentifier(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "provider-req-42")
		_, _ = w.Write([]byte(`{"id":"chat-1","model":"qwen3.8-flash",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}}`))
	}))
	defer server.Close()

	client, err := NewRemoteAPIChat(&ChatConfig{
		Source:    types.ModelSourceRemote,
		BaseURL:   server.URL,
		ModelName: "qwen3.8-flash",
		APIKey:    "test-key",
		Provider:  "deepseek",
	})
	require.NoError(t, err)

	resp, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, &ChatOptions{})
	require.NoError(t, err)
	require.NotNil(t, resp.ProviderCall, "a provider call must carry what the provider said about the call")
	assert.Equal(t, "provider-req-42", resp.ProviderCall.RequestID)
	assert.Equal(t, "X-Request-Id", resp.ProviderCall.RequestIDHeader)
	assert.Equal(t, server.URL+"/chat/completions", resp.ProviderCall.Endpoint)
	assert.NotEmpty(t, resp.ProviderCall.RequestFingerprint)
}

// A provider that sends no identifier leaves an empty one. The record must say
// so plainly and carry the basis a reader can check instead.
func TestProviderTraceSectionSaysWhenTheProviderSentNoIdentifier(t *testing.T) {
	call := types.ProviderCall{
		Endpoint:           "https://provider.invalid/v1/chat/completions",
		RequestFingerprint: "abc123",
	}
	section := buildProviderTraceSection(call, "qwen3.8-flash",
		types.TokenUsage{PromptTokens: 11, CompletionTokens: 2, TotalTokens: 13}, time.Now())

	assert.Contains(t, section, "provider 未提供请求标识")
	assert.Contains(t, section, "Endpoint: https://provider.invalid/v1/chat/completions")
	assert.Contains(t, section, "Model: qwen3.8-flash")
	assert.Contains(t, section, "RequestFingerprint: sha256:abc123")
	assert.Contains(t, section, "ObservedAt: ")
	assert.Contains(t, section, "Usage: Prompt: 11, Completion: 2, Total: 13")
}

// The trace is evidence a reviewer reads: it carries the endpoint, the model
// and a digest, and never a credential, a cookie or the prompt itself.
func TestTheProviderCallCarriesNoCredentialAndNoQueryString(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("X-Request-Id", "req-1")
	resp.Header.Set("Set-Cookie", "session=super-secret")
	resp.Header.Set("Authorization", "Bearer super-secret")

	call := providerCallFromResponse(resp,
		"https://provider.invalid/v1/chat/completions?api_key=super-secret",
		[]byte(`{"model":"qwen3.8-flash","prompt":"super-secret"}`))

	assert.Equal(t, "https://provider.invalid/v1/chat/completions", call.Endpoint)
	blob := call.RequestID + call.RequestIDHeader + call.Endpoint + call.RequestFingerprint
	assert.NotContains(t, blob, "super-secret")
	assert.NotContains(t, blob, "api_key")
}
