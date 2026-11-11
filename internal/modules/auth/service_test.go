package auth

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterValidation(t *testing.T) {
	svc := &Service{} // validation fails before the repository is used
	cases := []struct {
		name  string
		input RegisterInput
		want  error
	}{
		{"short handle", RegisterInput{Handle: "ab", Password: "password123"}, ErrInvalidHandle},
		{"bad handle chars", RegisterInput{Handle: "bad handle", Password: "password123"}, ErrInvalidHandle},
		{"short password", RegisterInput{Handle: "alice", Password: "1234567"}, ErrWeakPassword},
		{"bad email", RegisterInput{Handle: "alice", Password: "password123", Email: "nope"}, ErrInvalidEmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Register(context.Background(), tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAuthenticateRejectsEmptyToken(t *testing.T) {
	svc := &Service{}
	if _, err := svc.Authenticate(context.Background(), ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want unauthorized", err)
	}
}
