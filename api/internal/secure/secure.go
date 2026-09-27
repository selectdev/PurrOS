// Package secure handles API keys, secret encryption and webhook signatures.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const (
	APIKeyPrefix        = "pk_live_"
	WebhookSecretPrefix = "whsec_"
	base62              = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// RandomToken returns n random base62 characters.
func RandomToken(n int) string {
	var b strings.Builder
	max := big.NewInt(int64(len(base62)))
	for range n {
		i, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b.WriteByte(base62[i.Int64()])
	}
	return b.String()
}

// NewAPIKey returns a new plaintext API key, its hash for storage, and a
// display prefix that is safe to show in the UI.
func NewAPIKey() (plain string, hash []byte, display string) {
	plain = APIKeyPrefix + RandomToken(40)
	return plain, HashAPIKey(plain), plain[:len(APIKeyPrefix)+6]
}

// HashAPIKey hashes a key for lookup. Keys are high-entropy random tokens, so
// a fast hash is appropriate (unlike passwords).
func HashAPIKey(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}

// NewWebhookSecret returns a new webhook signing secret.
func NewWebhookSecret() string {
	return WebhookSecretPrefix + RandomToken(40)
}

// Box encrypts and decrypts small secrets (integration config, webhook
// secrets) with AES-256-GCM using a key derived from PURROS_SECRET.
type Box struct{ aead cipher.AEAD }

func NewBox(masterSecret string) (*Box, error) {
	key, err := hkdf.Key(sha256.New, []byte(masterSecret), nil, "purros:secrets:v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext. Output is version byte || nonce || ciphertext.
func (b *Box) Seal(plaintext []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	out := append([]byte{1}, nonce...)
	return b.aead.Seal(out, nonce, plaintext, nil)
}

func (b *Box) Open(sealed []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(sealed) < 1+ns || sealed[0] != 1 {
		return nil, errors.New("secure: unsupported ciphertext")
	}
	return b.aead.Open(nil, sealed[1:1+ns], sealed[1+ns:], nil)
}

// SignWebhook returns the PurrOS-Signature header value for a payload.
func SignWebhook(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	return "t=" + ts + ",v1=" + webhookMAC(secret, ts, body)
}

// VerifyWebhook checks a PurrOS-Signature header against a raw body.
func VerifyWebhook(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return errors.New("malformed signature header")
	}
	if d := now.Sub(time.Unix(unix, 0)); d > tolerance || d < -tolerance {
		return fmt.Errorf("signature timestamp outside tolerance (%s)", d)
	}
	if !hmac.Equal([]byte(sig), []byte(webhookMAC(secret, ts, body))) {
		return errors.New("signature mismatch")
	}
	return nil
}

func webhookMAC(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// Fingerprint identifies a PURROS_SECRET without revealing it, so a backup can
// tell whether it's being restored with the secret it was made with.
func Fingerprint(masterSecret string) string {
	m := hmac.New(sha256.New, []byte(masterSecret))
	m.Write([]byte("purros secret fingerprint v1"))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// SealedColumn is a bytea column holding values sealed with a Box.
type SealedColumn struct {
	Table, Key, Column string
}

// SealedColumns lists every column encrypted with PURROS_SECRET, for secret
// rotation and checks. Key is the table's primary key column.
var SealedColumns = []SealedColumn{
	{"integrations", "id", "config_encrypted"},
	{"webhook_endpoints", "id", "secret_encrypted"},
	{"users", "id", "totp_secret"},
}
