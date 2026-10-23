package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// providerRequestIDHeaders are the response headers a provider may use for its
// own request identifier.
//
// The list is an allowlist, not a copy-everything rule. A response header can
// carry a session token or a signed URL, and this record ends up in evidence a
// reviewer reads; only the headers known to hold a request id are read, and
// only the first non-empty one is kept.
var providerRequestIDHeaders = []string{
	"X-Request-Id",      // Aliyun Bailian / DashScope, OpenAI
	"Request-Id",        // generic spelling
	"Openai-Request-Id", // OpenAI-compatible gateways
	"X-Amzn-Requestid",  // AWS Bedrock
	"X-Goog-Request-Id", // Google
	"Cf-Ray",            // Cloudflare
}

// providerCallFromResponse reads the provider's own facts about one call.
//
// It reads three things: the request identifier (when the provider sent one),
// the endpoint, and the fingerprint of the request body. The body itself is
// never kept -- only its digest is.
//
// A provider that sends no identifier is not an error here: the result simply
// carries an empty RequestID, and the caller decides what to record about
// that. Nothing here invents an identifier.
func providerCallFromResponse(resp *http.Response, endpoint string, body []byte) *types.ProviderCall {
	call := &types.ProviderCall{
		Endpoint:           endpointWithoutQuery(endpoint),
		RequestFingerprint: requestFingerprint(body),
	}
	if resp != nil {
		for _, name := range providerRequestIDHeaders {
			if value := strings.TrimSpace(resp.Header.Get(name)); value != "" {
				call.RequestID = value
				call.RequestIDHeader = name
				break
			}
		}
	}
	return call
}

// endpointWithoutQuery keeps the endpoint identifiable without keeping its
// query string.
//
// A query string can carry a token, a signature or an account name. The
// endpoint is recorded so a reader can tell which provider answered; the
// query is not needed for that and is not kept.
func endpointWithoutQuery(endpoint string) string {
	if index := strings.IndexByte(endpoint, '?'); index >= 0 {
		return endpoint[:index]
	}
	return endpoint
}

// requestFingerprint is the lowercase hex SHA-256 of a request body.
//
// Two calls with the same fingerprint sent the same bytes. That is what makes
// a request checkable against a provider's own records without keeping the
// prompt in the record.
func requestFingerprint(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
