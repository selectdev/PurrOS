package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/selectdev/purros/api/internal/secure"
)

const totpStep = 30 * time.Second

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a new base32 TOTP secret (160 bits).
func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// TOTPURL returns the otpauth:// URL shown as a QR code by authenticator apps.
func TOTPURL(issuer, account, secret string) string {
	v := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

// TOTPCode returns the 6-digit code for a time step (RFC 6238).
func TOTPCode(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000), nil
}

// TOTPStep returns the time step for t.
func TOTPStep(t time.Time) int64 { return t.Unix() / int64(totpStep/time.Second) }

// VerifyTOTP checks code against the steps around now (±30 s for clock drift)
// and returns the matching step, which must be greater than lastStep so a
// code can't be used twice.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	cur := TOTPStep(now)
	for _, s := range []int64{cur - 1, cur, cur + 1} {
		if s <= lastStep {
			continue
		}
		want, err := TOTPCode(secret, s)
		if err == nil && hmac.Equal([]byte(want), []byte(code)) {
			return s, true
		}
	}
	return 0, false
}

// NewRecoveryCodes returns n codes formatted as xxxxx-xxxxx.
func NewRecoveryCodes(n int) []string {
	out := make([]string, n)
	for i := range out {
		t := strings.ToLower(secure.RandomToken(10)) // ~50 bits each
		out[i] = t[:5] + "-" + t[5:]
	}
	return out
}

// HashToken hashes a high-entropy token for storage.
func HashToken(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// HashRecoveryCode hashes a recovery code, ignoring case and spacing.
func HashRecoveryCode(s string) []byte {
	return HashToken(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "")))
}

// NewToken returns a random single-use token and its hash.
func NewToken() (string, []byte) {
	t := secure.RandomToken(43)
	return t, HashToken(t)
}
