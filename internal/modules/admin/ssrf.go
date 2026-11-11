package admin

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// validateConnectorURL enforces the SSRF boundary (开发技术规范 §4.6):
// only HTTP(S); loopback, link-local, private, multicast and cloud metadata
// addresses are rejected unless the host is explicitly allow-listed.
func validateConnectorURL(raw string) error { return ValidateRemoteURL(raw) }

// ValidateRemoteURL is shared with the import Worker so that every redirect
// target is re-checked against the same SSRF boundary (§4.6).
func ValidateRemoteURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil // third-party API connectors may not need a base URL
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: 地址格式无效", ErrInvalidInput)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: 只接受 HTTP / HTTPS", ErrInvalidInput)
	}
	if parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("%w: 地址不得包含凭据或缺少主机", ErrInvalidInput)
	}
	host := strings.ToLower(parsed.Hostname())
	for _, trusted := range trustedImportHosts() {
		if strings.EqualFold(host, trusted) {
			return nil
		}
	}

	addresses := []net.IP{}
	if ip := net.ParseIP(host); ip != nil {
		addresses = append(addresses, ip)
	} else {
		lookupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resolved, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
		if err != nil || len(resolved) == 0 {
			return fmt.Errorf("%w: 无法解析主机，已拒绝", ErrInvalidInput)
		}
		for _, item := range resolved {
			addresses = append(addresses, item.IP)
		}
	}
	for _, ip := range addresses {
		if forbiddenIP(ip) {
			return fmt.Errorf("%w: 目标地址属于禁止访问的网络范围", ErrInvalidInput)
		}
	}
	return nil
}

func forbiddenIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return true
	}
	// 云厂商元数据地址（含 IPv4 与常见兼容端点）。
	for _, blocked := range []string{
		"169.254.169.254", "100.100.100.200", "192.0.0.192",
		"fd00:ec2::254", "fe80::", "::1",
	} {
		if ip.Equal(net.ParseIP(blocked)) {
			return true
		}
	}
	return false
}

// trustedImportHosts is injected from config at process start; an empty list
// means no private network is trusted (default deny).
var importTrustedHosts []string

func SetTrustedImportHosts(hosts []string) { importTrustedHosts = hosts }

func trustedImportHosts() []string { return importTrustedHosts }
