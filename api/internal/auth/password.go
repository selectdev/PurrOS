// Package auth holds the building blocks of signing in: password hashing,
// one-time codes (TOTP), and single-use tokens.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP's recommended minimum: 19 MiB, 2 passes).
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16

	MinPasswordLength = 10
	MaxPasswordLength = 256
)

// CheckPasswordPolicy returns a user-facing reason a password is rejected, or "".
func CheckPasswordPolicy(pw, email string) string {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPasswordLength:
		return fmt.Sprintf("Must be at least %d characters", MinPasswordLength)
	case n > MaxPasswordLength:
		return fmt.Sprintf("Must be at most %d characters", MaxPasswordLength)
	case email != "" && strings.EqualFold(pw, email):
		return "Must not be your email address"
	case commonPasswords[strings.ToLower(pw)]:
		return "This password is too common"
	}
	return ""
}

var commonPasswords = map[string]bool{
	"password123": true, "password1234": true, "1234567890": true, "12345678910": true, "qwertyuiop": true,
	"iloveyou123": true, "1q2w3e4r5t": true, "qwerty12345": true, "abcdefghij": true, "passw0rd123": true,
	"administrator": true, "letmein1234": true, "welcome123": true, "0987654321": true, "1111111111": true,
}

// HashPassword returns an encoded Argon2id hash.
func HashPassword(pw string) string {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key))
}

// VerifyPassword checks pw against an encoded hash in constant time.
func VerifyPassword(encoded, pw string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var version int
	var mem, iters uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iters, &threads); err != nil {
		return false, errors.New("bad argon2 parameters")
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, iters, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified against when an account doesn't exist, so response
// times don't reveal which emails have accounts.
var dummyHash = HashPassword("purros-dummy-password")

// BurnTime spends the same time as a real password check.
func BurnTime(pw string) { _, _ = VerifyPassword(dummyHash, pw) }
