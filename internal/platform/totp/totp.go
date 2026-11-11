// Package totp implements RFC 6238 time-based one-time passwords
// (HMAC-SHA1, 30s step, 6 digits, ±1 step clock window) for admin 2FA (§11.6).
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

const (
	step      = 30 * time.Second
	secretLen = 20 // 160-bit secret，Base32 后 32 字符
	digits    = 1000000
)

// GenerateSecret 返回 Base32（无填充）编码的随机密钥。
func GenerateSecret() (string, error) {
	buf := make([]byte, secretLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("totp: generate secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// Validate 校验验证码；允许前后各一个步长的时钟漂移。
func Validate(secret, code string, now time.Time) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(
		strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil || len(key) == 0 {
		return false
	}
	counter := uint64(now.Unix()) / uint64(step.Seconds())
	for _, offset := range []uint64{0, 1, ^uint64(0)} {
		if hotp(key, counter+offset) == code {
			return true
		}
	}
	return false
}

// hotp 是 RFC 4226：HMAC-SHA1 截断为 6 位数字。
func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff) % digits
	return fmt.Sprintf("%06d", value)
}

// OTPAuthURI 生成认证器 App 可扫描的 otpauth:// 链接。
func OTPAuthURI(secret, issuer, account string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		issuer, account, secret, issuer)
}
