// Package httpx 提供跨模块共用的 HTTP 请求辅助。
package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
)

// trustedProxies 是启动时写入一次的只读配置（api 与 worker 各自一份进程）。
// 空列表 = 不信任任何代理头，只读 TCP 对端地址。
var trustedProxies atomic.Value // []netip.Prefix

// SetTrustedProxies 只在进程启动时调用一次。
// 传空表示「只认服务器看到的原始地址」——这也是默认行为。
func SetTrustedProxies(prefixes []netip.Prefix) {
	list := make([]netip.Prefix, len(prefixes))
	copy(list, prefixes)
	trustedProxies.Store(list)
}

func trustedList() []netip.Prefix {
	if value, ok := trustedProxies.Load().([]netip.Prefix); ok {
		return value
	}
	return nil
}

// ClientIP 返回访客地址，只有两个可信来源：
//
//  1. TCP 对端地址（r.RemoteAddr）——服务器自己看到的，客户端改不了；
//  2. 当且仅当直连对端落在受信代理网段内时，取 X-Forwarded-For 的
//     **最右一跳非受信地址**。代理会把真实客户端追加在右侧，左侧可能是
//     客户端自己伪造的内容，所以从右往左取，绝不能取最左。
//
// X-Real-IP 一律不读：它不是标准头，任何客户端都能随便填。
func ClientIP(r *http.Request) string {
	peer, ok := parseAddr(r.RemoteAddr)
	if !ok {
		return ""
	}
	trusted := trustedList()
	if len(trusted) == 0 || !contains(trusted, peer) {
		return peer.String()
	}
	for _, header := range r.Header.Values("X-Forwarded-For") {
		parts := strings.Split(header, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			addr, ok := parseAddr(parts[i])
			if !ok {
				continue
			}
			if contains(trusted, addr) {
				continue
			}
			return addr.String()
		}
	}
	return peer.String()
}

// parseAddr 接受 "1.2.3.4:80"、"[2001:db8::1]:443"、裸 IP，以及带 zone 的 IPv6。
func parseAddr(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	if index := strings.LastIndex(raw, "%"); index >= 0 {
		raw = raw[:index]
	}
	addr, err := netip.ParseAddr(strings.Trim(raw, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true // IPv4-mapped IPv6 统一成 IPv4，限流桶才不会分裂
}

func contains(list []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range list {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
