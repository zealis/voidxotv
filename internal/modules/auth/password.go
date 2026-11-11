// Package auth implements local accounts, Redis-backed sessions and CSRF protection.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024 // 64 MiB
	argonIterations  = 3
	argonParallelism = 2
	argonSaltBytes   = 16
	argonKeyBytes    = 32
)

var ErrMalformedHash = errors.New("malformed password hash")

// HashPassword returns a self-describing Argon2id hash (开发技术规范 §11.4).
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword compares in constant time. A malformed hash is an error, never a match.
func VerifyPassword(hash, password string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, ErrMalformedHash
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, ErrMalformedHash
	}
	memory, err := parseParam(params[0], "m")
	if err != nil {
		return false, err
	}
	iterations, err := parseParam(params[1], "t")
	if err != nil {
		return false, err
	}
	parallelism, err := parseParam(params[2], "p")
	if err != nil {
		return false, err
	}
	if memory == 0 || iterations == 0 || parallelism == 0 || memory > 1<<20 || parallelism > 255 {
		return false, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrMalformedHash
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) == 0 {
		return false, ErrMalformedHash
	}
	actual := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func parseParam(raw, prefix string) (uint64, error) {
	value, err := strconv.ParseUint(strings.TrimPrefix(raw, prefix+"="), 10, 32)
	if err != nil {
		return 0, ErrMalformedHash
	}
	return value, nil
}
