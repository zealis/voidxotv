package auth

import (
	"context"
	"time"
)

// Principal is the verified identity attached to a request context.
type Principal struct {
	UserID       int64
	Handle       string
	Roles        []string
	DeviceID     string
	SessionToken string
	CSRF         string
}

type principalKey struct{}

// WithPrincipal attaches a verified identity; only RequireAuth should call it.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// PrincipalFromContext returns the identity injected by RequireAuth.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

// SessionRecord is the Redis-side active session; the database keeps only a hash for audit.
type SessionRecord struct {
	UserID            int64     `json:"user_id"`
	Handle            string    `json:"handle"`
	Roles             []string  `json:"roles"`
	DeviceID          string    `json:"device_id"`
	CSRF              string    `json:"csrf"`
	CreatedAt         time.Time `json:"created_at"`
	AbsoluteExpiresAt time.Time `json:"absolute_expires_at"`
}

// User is the account row; PasswordHash never leaves the repository.
type User struct {
	ID           int64
	Handle       string
	Email        string
	PasswordHash string
	Role         string
	Status       string
	CreatedAt    time.Time
}
