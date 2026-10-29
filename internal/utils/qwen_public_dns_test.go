package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestQwenDNSFallbackRequiresExplicitConfigAndOnlyFakeAnswers(t *testing.T) {
	fake := net.IPAddr{IP: net.ParseIP("198.18.0.1")}
	local := net.IPAddr{IP: net.ParseIP("127.0.0.1")}
	public := net.IPAddr{IP: net.ParseIP("8.8.8.8")}
	tests := []struct {
		name, host, endpoint string
		answers              []net.IPAddr
		wantCalls            int
		wantErr              bool
	}{
		{"disabled by default", "dashscope.aliyuncs.com", "", []net.IPAddr{fake}, 0, false},
		{"unrelated host", "attacker.example", "https://dns.example/query", []net.IPAddr{fake}, 0, false},
		{"mixed restricted answers", "dashscope.aliyuncs.com", "https://dns.example/query", []net.IPAddr{fake, local}, 0, false},
		{"mixed public and fake answers", "dashscope.aliyuncs.com", "https://dns.example/query", []net.IPAddr{fake, public}, 0, false},
		{"ordinary public resolution", "dashscope.aliyuncs.com", "https://dns.example/query", []net.IPAddr{public}, 0, false},
		{"opted in fake DNS", "DASHSCOPE.ALIYUNCS.COM.", "https://dns.example/query", []net.IPAddr{fake}, 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LINGDOC_QWEN_DOH_URL", tc.endpoint)
			t.Setenv("LINGDOC_QWEN_DOH_BOOTSTRAP_IP", "8.8.8.8")
			t.Setenv("HTTPS_PROXY", "")
			t.Setenv("https_proxy", "")
			t.Setenv("HTTP_PROXY", "")
			t.Setenv("http_proxy", "")
			calls := 0
			got, err := resolveForSSRFWith(context.Background(), tc.host, func(context.Context, string) ([]net.IPAddr, error) {
				return tc.answers, nil
			}, func(_ context.Context, _, _, _ string) ([]net.IPAddr, error) {
				calls++
				return []net.IPAddr{public}, nil
			})
			if (err != nil) != tc.wantErr || calls != tc.wantCalls {
				t.Fatalf("err=%v fallback calls=%d, want %d", err, calls, tc.wantCalls)
			}
			if tc.wantCalls == 1 && (len(got) != 1 || !got[0].IP.Equal(public.IP)) {
				t.Fatalf("fallback answers=%v", got)
			}
			if tc.wantCalls == 0 {
				if len(got) != len(tc.answers) {
					t.Fatalf("system answer count changed: %v", got)
				}
				for i := range got {
					if !got[i].IP.Equal(tc.answers[i].IP) {
						t.Fatalf("system answer changed: %v", got)
					}
				}
			}
		})
	}
}

// Opt-in network regression: the system resolver must actually be returning
// fake addresses, and both URL validation and the final pinned TLS connection
// must succeed through the configured DNS fallback.
func TestQwenFakeIPLiveOptIn(t *testing.T) {
	if os.Getenv("LINGDOC_QWEN_DOH_LIVE_TEST") != "1" {
		t.Skip("requires explicit Qwen fake-IP network test")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), "dashscope.aliyuncs.com")
	if err != nil || !allFakeIPs(ips) {
		t.Fatalf("expected actual fake-IP system resolution, got %v, %v", ips, err)
	}
	if err := ValidateURLForSSRF("https://dashscope.aliyuncs.com/"); err != nil {
		t.Fatalf("URL validation with configured resolver: %v", err)
	}
	client := NewSSRFSafeHTTPClient(SSRFSafeHTTPClientConfig{Timeout: 20 * time.Second, MaxRedirects: 0})
	resp, err := client.Get("https://dashscope.aliyuncs.com/")
	if err != nil {
		t.Fatalf("validated pinned TLS request: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close Qwen response: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected Qwen root HTTP status %d", resp.StatusCode)
	}
}

func TestQwenDNSFallbackDoesNotHideResolverFailures(t *testing.T) {
	t.Setenv("LINGDOC_QWEN_DOH_URL", "https://dns.example/query")
	t.Setenv("LINGDOC_QWEN_DOH_BOOTSTRAP_IP", "8.8.8.8")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	calls := 0
	_, err := resolveForSSRFWith(context.Background(), "dashscope.aliyuncs.com", func(context.Context, string) ([]net.IPAddr, error) {
		return nil, errors.New("system DNS failed")
	}, func(context.Context, string, string, string) ([]net.IPAddr, error) {
		calls++
		return nil, nil
	})
	if err == nil || calls != 0 {
		t.Fatalf("system failure err=%v fallback calls=%d", err, calls)
	}
	_, err = resolveForSSRFWith(context.Background(), "dashscope.aliyuncs.com", func(context.Context, string) ([]net.IPAddr, error) {
		return nil, nil
	}, func(context.Context, string, string, string) ([]net.IPAddr, error) {
		calls++
		return nil, nil
	})
	if err == nil || calls != 0 {
		t.Fatalf("empty system answer err=%v fallback calls=%d", err, calls)
	}
	_, err = resolveForSSRFWith(context.Background(), "dashscope.aliyuncs.com", func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("198.18.0.1")}}, nil
	}, func(context.Context, string, string, string) ([]net.IPAddr, error) {
		return nil, errors.New("HTTPS DNS failed")
	})
	if err == nil {
		t.Fatal("HTTPS DNS failure must remain an error")
	}
}

func TestQwenDNSFallbackRefusesUnpinnedProxy(t *testing.T) {
	t.Setenv("LINGDOC_QWEN_DOH_URL", "https://dns.example/query")
	t.Setenv("LINGDOC_QWEN_DOH_BOOTSTRAP_IP", "8.8.8.8")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9999")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	calls := 0
	_, err := resolveForSSRFWith(context.Background(), "dashscope.aliyuncs.com", func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("198.18.0.1")}}, nil
	}, func(context.Context, string, string, string) ([]net.IPAddr, error) {
		calls++
		return nil, nil
	})
	if err == nil || calls != 0 {
		t.Fatalf("proxy must be refused before DNS fallback: err=%v calls=%d", err, calls)
	}
}

func TestQwenDoHRejectsUnsafeConfiguration(t *testing.T) {
	for _, tc := range []struct{ endpoint, bootstrap string }{
		{"http://dns.example/query", "8.8.8.8"},
		{"https://127.0.0.1/query", "8.8.8.8"},
		{"https://user:password@dns.example/query", "8.8.8.8"},
		{"https://dns.example:8080/query", "8.8.8.8"},
		{"https://dns.example/query?x=y", "8.8.8.8"},
		{"https://dns.example/query", "127.0.0.1"},
		{"https://dns.example/query", "198.18.0.2"},
		{"https://dns.example/query", ""},
	} {
		if _, err := resolveQwenDoH(context.Background(), "dashscope.aliyuncs.com", tc.endpoint, tc.bootstrap); err == nil {
			t.Errorf("unsafe configuration accepted: endpoint=%q bootstrap=%q", tc.endpoint, tc.bootstrap)
		}
	}
}

func TestQwenDoHChecksEveryReturnedAddress(t *testing.T) {
	for _, tc := range []struct {
		name, a, aaaa string
		wantErr       bool
	}{
		{"public v4 and v6", `{"Status":0,"Answer":[{"type":1,"data":"8.8.8.8"}]}`, `{"Status":0,"Answer":[{"type":28,"data":"2606:4700:4700::1111"}]}`, false},
		{"empty IPv6", `{"Status":0,"Answer":[{"type":1,"data":"8.8.8.8"}]}`, `{"Status":0}`, false},
		{"private alongside public", `{"Status":0,"Answer":[{"type":1,"data":"8.8.8.8"},{"type":1,"data":"127.0.0.1"}]}`, `{"Status":0}`, true},
		{"fake answer", `{"Status":0,"Answer":[{"type":1,"data":"198.18.0.1"}]}`, `{"Status":0}`, true},
		{"invalid IP", `{"Status":0,"Answer":[{"type":1,"data":"not-an-ip"}]}`, `{"Status":0}`, true},
		{"bad status", `{"Status":3}`, `{"Status":0}`, true},
		{"missing status", `{"Answer":[{"type":1,"data":"8.8.8.8"}]}`, `{"Status":0}`, true},
		{"malformed JSON", `{"Status":`, `{"Status":0}`, true},
		{"restricted IPv6", `{"Status":0,"Answer":[{"type":1,"data":"8.8.8.8"}]}`, `{"Status":0,"Answer":[{"type":28,"data":"::1"}]}`, true},
		{"no addresses", `{"Status":0}`, `{"Status":0}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("name") != "dashscope.aliyuncs.com" || r.Header.Get("Accept") != "application/dns-json" {
					t.Error("unexpected DNS query")
				}
				body := tc.aaaa
				if r.URL.Query().Get("type") == "A" {
					body = tc.a
				}
				if _, err := fmt.Fprint(w, body); err != nil {
					t.Errorf("write DNS response: %v", err)
				}
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL + "/dns-query")
			ips, err := queryQwenDoH(context.Background(), server.Client(), u, "dashscope.aliyuncs.com")
			if (err != nil) != tc.wantErr {
				t.Fatalf("ips=%v err=%v wantErr=%v", ips, err, tc.wantErr)
			}
		})
	}
}
