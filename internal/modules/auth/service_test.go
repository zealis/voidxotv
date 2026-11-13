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
		{"missing email", RegisterInput{Password: "password123"}, ErrInvalidEmail},
		{"bad email", RegisterInput{Email: "nope", Password: "password123"}, ErrInvalidEmail},
		{"short password", RegisterInput{Email: "alice@example.com", Password: "1234567"}, ErrWeakPassword},
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
