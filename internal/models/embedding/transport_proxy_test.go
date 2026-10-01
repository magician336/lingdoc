package embedding

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEmbeddingHTTPClientUsesConfiguredProxy(t *testing.T) {
	if os.Getenv("LINGDOC_EMBED_PROXY_TEST_CHILD") == "1" {
		response, err := newEmbeddingHTTPClient(3*time.Second).Post("http://example.com/embedding-proxy-check", "text/plain", strings.NewReader("embedding-request"))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "proxy-response" {
			t.Fatalf("unexpected response: status=%d body=%q err=%v", response.StatusCode, body, err)
		}
		return
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Host != "example.com" || r.URL.Path != "/embedding-proxy-check" || string(body) != "embedding-request" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "proxy-response")
	}))
	defer proxy.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestEmbeddingHTTPClientUsesConfiguredProxy$")
	for _, v := range os.Environ() {
		k := strings.ToUpper(strings.SplitN(v, "=", 2)[0])
		if k != "HTTP_PROXY" && k != "HTTPS_PROXY" && k != "ALL_PROXY" && k != "NO_PROXY" && k != "LINGDOC_EMBED_PROXY_TEST_CHILD" {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "HTTP_PROXY="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "NO_PROXY=", "LINGDOC_EMBED_PROXY_TEST_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("proxy request failed: %v\n%s", err, out)
	}
}
