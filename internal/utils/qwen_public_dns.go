package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// resolveForSSRF normally uses the system resolver. Operators may explicitly
// configure an HTTPS DNS endpoint and a public bootstrap IP for the official
// Qwen host when a local TUN resolver returns only fake addresses. This is a
// resolver choice, not an SSRF allowlist: the returned IPs are still checked
// by the caller, and the final dial is pinned to the checked answers.
func resolveForSSRF(ctx context.Context, hostname string) ([]net.IPAddr, error) {
	return resolveForSSRFWith(ctx, hostname, net.DefaultResolver.LookupIPAddr, resolveQwenDoH)
}

func resolveForSSRFWith(ctx context.Context, hostname string, system func(context.Context, string) ([]net.IPAddr, error), fallback func(context.Context, string, string, string) ([]net.IPAddr, error)) ([]net.IPAddr, error) {
	ips, err := system(ctx, hostname)
	if err != nil {
		return ips, err
	}
	if len(ips) == 0 {
		return nil, errors.New("DNS resolution returned no addresses")
	}
	// Do not reinterpret mixed answers or any private, loopback, link-local or
	// other restricted address as a TUN response.
	if !strings.EqualFold(strings.TrimSuffix(hostname, "."), "dashscope.aliyuncs.com") || !allFakeIPs(ips) {
		return ips, nil
	}
	endpoint := strings.TrimSpace(os.Getenv("LINGDOC_QWEN_DOH_URL"))
	if endpoint == "" {
		return ips, nil
	}
	// A CONNECT proxy resolves its own target, so it cannot guarantee that the
	// address validated here is the address it connects to. Require a direct
	// route (or an explicit NO_PROXY entry) for the fallback path.
	proxy, proxyErr := httpproxy.FromEnvironment().ProxyFunc()(&url.URL{Scheme: "https", Host: hostname})
	if proxyErr != nil {
		return nil, fmt.Errorf("Qwen HTTPS DNS proxy configuration: %w", proxyErr)
	}
	if proxy != nil {
		return nil, errors.New("Qwen HTTPS DNS fallback requires a direct connection; configure NO_PROXY for the Qwen host")
	}
	bootstrap := strings.TrimSpace(os.Getenv("LINGDOC_QWEN_DOH_BOOTSTRAP_IP"))
	return fallback(ctx, hostname, endpoint, bootstrap)
}

func allFakeIPs(ips []net.IPAddr) bool {
	if len(ips) == 0 {
		return false
	}
	for _, item := range ips {
		ip := item.IP.To4()
		if ip == nil || ip[0] != 198 || (ip[1] != 18 && ip[1] != 19) {
			return false
		}
	}
	return true
}

// resolveQwenDoH uses an operator-selected HTTPS DNS JSON endpoint. It never
// resolves the DNS endpoint through the fake resolver: TLS is verified for
// its configured hostname while the TCP connection is pinned to the explicit
// public bootstrap address. Redirects and non-public answers are refused.
func resolveQwenDoH(ctx context.Context, hostname, endpoint, bootstrap string) ([]net.IPAddr, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || net.ParseIP(u.Hostname()) != nil || isIPLikeHostname(u.Hostname()) {
		return nil, errors.New("Qwen HTTPS DNS endpoint must be an HTTPS hostname without credentials, query, or nonstandard port")
	}
	addr := net.ParseIP(bootstrap)
	if addr == nil {
		return nil, errors.New("Qwen HTTPS DNS bootstrap must be a public IP")
	}
	if restricted, _ := isRestrictedIP(addr); restricted {
		return nil, errors.New("Qwen HTTPS DNS bootstrap must be a public IP")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), "443"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return queryQwenDoH(ctx, client, u, hostname)
}

func queryQwenDoH(ctx context.Context, client *http.Client, u *url.URL, hostname string) ([]net.IPAddr, error) {
	out := []net.IPAddr{}
	for _, queryType := range []string{"A", "AAAA"} {
		q := *u
		values := url.Values{"name": {strings.TrimSuffix(hostname, ".")}, "type": {queryType}}
		q.RawQuery = values.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, q.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/dns-json")
		response, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Qwen HTTPS DNS lookup failed: %w", err)
		}
		if response.StatusCode != http.StatusOK {
			if err := response.Body.Close(); err != nil {
				return nil, fmt.Errorf("Qwen HTTPS DNS returned HTTP %d; closing response: %w", response.StatusCode, err)
			}
			return nil, fmt.Errorf("Qwen HTTPS DNS returned HTTP %d", response.StatusCode)
		}
		var data struct {
			Status *int `json:"Status"`
			Answer []struct {
				Type int    `json:"type"`
				Data string `json:"data"`
			} `json:"Answer"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&data)
		closeErr := response.Body.Close()
		if err != nil || data.Status == nil || *data.Status != 0 {
			return nil, errors.New("Qwen HTTPS DNS returned an invalid or unsuccessful response")
		}
		if closeErr != nil {
			return nil, fmt.Errorf("Qwen HTTPS DNS closing response: %w", closeErr)
		}
		for _, answer := range data.Answer {
			if answer.Type != 1 && answer.Type != 28 {
				continue
			}
			ip := net.ParseIP(answer.Data)
			if ip == nil {
				return nil, errors.New("Qwen HTTPS DNS returned an invalid IP")
			}
			if restricted, _ := isRestrictedIP(ip); restricted {
				return nil, errors.New("Qwen HTTPS DNS returned a restricted IP")
			}
			out = append(out, net.IPAddr{IP: ip})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("Qwen HTTPS DNS returned no public IP")
	}
	return out, nil
}
