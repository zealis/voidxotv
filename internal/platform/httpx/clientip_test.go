package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func prefixes(t *testing.T, raw ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(raw))
	for _, item := range raw {
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			t.Fatalf("bad prefix %q: %v", item, err)
		}
		out = append(out, prefix)
	}
	return out
}

func request(remote string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	return r
}

// 默认（没有配置受信代理）：任何代理头都不看，只认 TCP 对端。
func TestClientIPIgnoresForwardedHeadersByDefault(t *testing.T) {
	SetTrustedProxies(nil)
	r := request("203.0.113.5:5555", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
		"X-Real-IP":       "8.8.8.8",
	})
	if got := ClientIP(r); got != "203.0.113.5" {
		t.Fatalf("ClientIP = %q, want peer address", got)
	}
}

func TestClientIPHandlesIPv6AndZone(t *testing.T) {
	SetTrustedProxies(nil)
	cases := map[string]string{
		"[2001:db8::1]:443":    "2001:db8::1",
		"2001:db8::1":          "2001:db8::1",
		"[fe80::1%eth0]:8080":  "fe80::1",
		"127.0.0.1:1234":       "127.0.0.1",
		"[::ffff:10.0.0.1]:80": "10.0.0.1",
	}
	for remote, want := range cases {
		if got := ClientIP(request(remote, nil)); got != want {
			t.Errorf("ClientIP(%q) = %q, want %q", remote, got, want)
		}
	}
}

// 受信代理：从右往左取第一个非受信地址，客户端塞在最左的伪造值必须被忽略。
func TestClientIPTakesRightmostUntrustedHop(t *testing.T) {
	SetTrustedProxies(prefixes(t, "10.0.0.0/8", "127.0.0.1/32"))
	cases := []struct {
		remote  string
		forward string
		want    string
	}{
		{"10.0.0.2:1234", "1.2.3.4", "1.2.3.4"},
		{"10.0.0.2:1234", "1.2.3.4, 203.0.113.9", "203.0.113.9"},
		{"10.0.0.2:1234", "203.0.113.9, 10.0.0.3", "203.0.113.9"},
		{"10.0.0.2:1234", "8.8.8.8, 203.0.113.9", "203.0.113.9"}, // 最左是伪造，取最右真实
		{"10.0.0.2:1234", "", "10.0.0.2"},                        // 没有头就退回对端
	}
	for _, tc := range cases {
		headers := map[string]string{}
		if tc.forward != "" {
			headers["X-Forwarded-For"] = tc.forward
		}
		if got := ClientIP(request(tc.remote, headers)); got != tc.want {
			t.Errorf("remote=%q xff=%q -> %q, want %q", tc.remote, tc.forward, got, tc.want)
		}
	}
}

// 直连对端不在受信网段时，即便带了 X-Forwarded-For 也不采信。
func TestClientIPIgnoresHeaderFromUntrustedPeer(t *testing.T) {
	SetTrustedProxies(prefixes(t, "10.0.0.0/8"))
	r := request("203.0.113.5:5555", map[string]string{"X-Forwarded-For": "1.2.3.4"})
	if got := ClientIP(r); got != "203.0.113.5" {
		t.Fatalf("ClientIP = %q, want untrusted peer address", got)
	}
}

func TestClientIPEmptyRemoteAddr(t *testing.T) {
	SetTrustedProxies(nil)
	if got := ClientIP(request("", nil)); got != "" {
		t.Fatalf("ClientIP = %q, want empty", got)
	}
}
