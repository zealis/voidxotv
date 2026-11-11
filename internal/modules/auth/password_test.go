package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected hash format: %s", hash[:40])
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("verify = %v, err = %v", ok, err)
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("password-one")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(hash, "password-two")
	if err != nil || ok {
		t.Fatalf("wrong password accepted: ok=%v err=%v", ok, err)
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, hash := range []string{"", "argon2$abc", "$argon2id$v=19$m=0,t=3,p=2$AA$AA"} {
		ok, err := VerifyPassword(hash, "anything")
		if ok || err == nil {
			t.Fatalf("hash %q should be rejected: ok=%v err=%v", hash, ok, err)
		}
	}
}

func TestTokenHashIsStableAndDistinct(t *testing.T) {
	a, b := TokenHash("token-a"), TokenHash("token-b")
	if len(a) != 32 || string(a) == string(b) {
		t.Fatal("token hashing is broken")
	}
	if string(a) == "token-a" {
		t.Fatal("raw token must not be stored")
	}
}
