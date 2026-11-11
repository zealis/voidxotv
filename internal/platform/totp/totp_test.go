package totp

import (
	"encoding/base32"
	"testing"
	"time"
)

func TestValidateAcceptsCurrentAndAdjacentSteps(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	code := hotpMust(t, secret, now)
	if !Validate(secret, code, now) {
		t.Fatalf("current step code should validate")
	}
	if !Validate(secret, code, now.Add(30*time.Second)) {
		t.Fatalf("next step should still validate (clock window)")
	}
	if Validate(secret, code, now.Add(91*time.Second)) {
		t.Fatalf("code beyond window must not validate")
	}
}

func TestValidateRejectsMalformedInput(t *testing.T) {
	secret := "MFRGGZDFMZTWQ2LK"
	if Validate(secret, "abcdef", time.Now()) {
		t.Fatal("non-digits must be rejected")
	}
	if Validate(secret, "12345", time.Now()) {
		t.Fatal("short codes must be rejected")
	}
	if Validate("!!!not-base32!!!", "123456", time.Now()) {
		t.Fatal("invalid secret must be rejected")
	}
}

func TestOTPAuthURIContainsParams(t *testing.T) {
	secret := "MFRGGZDFMZTWQ2LK"
	uri := OTPAuthURI(secret, "夜街后台", "admin")
	for _, want := range []string{"otpauth://totp/", "secret=MFRGGZDFMZTWQ2LK", "issuer=", "digits=6", "period=30"} {
		if !contains(uri, want) {
			t.Fatalf("uri %q missing %q", uri, want)
		}
	}
}

func hotpMust(t *testing.T, secret string, now time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return hotp(key, uint64(now.Unix())/30)
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 ||
		indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
