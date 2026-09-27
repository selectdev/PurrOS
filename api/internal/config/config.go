// Package config loads server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	URL         string // PURROS_URL
	Secret      string // PURROS_SECRET
	DatabaseURL string // DATABASE_URL
	RedisURL    string // REDIS_URL (optional)
	ListenAddr  string // PURROS_LISTEN (default :8080)
	LogLevel    string // LOG_LEVEL
	TrustProxy  bool   // PURROS_TRUST_PROXY: trust X-Forwarded-For from the reverse proxy

	RateLimitPerMin       int // API_RATE_LIMIT_PER_MIN
	IngestRateLimitPerMin int // API_INGEST_RATE_LIMIT_PER_MIN
	MaxBatchSize          int // API_MAX_BATCH_SIZE

	// RunWorker makes `purros serve` also run the background worker in-process.
	RunWorker bool // PURROS_RUN_WORKER (default true)
}

var insecureSecrets = map[string]bool{"": true, "change-me": true, "changeme": true, "secret": true}

// Load reads the configuration. requireSecret is false for commands that don't
// touch encrypted data (e.g. `migrate`).
func Load(requireSecret bool) (Config, error) {
	c := Config{
		URL:                   strings.TrimRight(env("PURROS_URL", "http://localhost:8080"), "/"),
		Secret:                os.Getenv("PURROS_SECRET"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		RedisURL:              os.Getenv("REDIS_URL"),
		ListenAddr:            env("PURROS_LISTEN", ":8080"),
		LogLevel:              env("LOG_LEVEL", "info"),
		TrustProxy:            envBool("PURROS_TRUST_PROXY", true),
		RateLimitPerMin:       envInt("API_RATE_LIMIT_PER_MIN", 600),
		IngestRateLimitPerMin: envInt("API_INGEST_RATE_LIMIT_PER_MIN", 3000),
		MaxBatchSize:          envInt("API_MAX_BATCH_SIZE", 1000),
		RunWorker:             envBool("PURROS_RUN_WORKER", true),
	}
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if requireSecret && (insecureSecrets[c.Secret] || len(c.Secret) < 32) {
		errs = append(errs, errors.New("PURROS_SECRET must be at least 32 characters (generate one with: openssl rand -base64 32)"))
	}
	return c, errors.Join(errs...)
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("%s must be an integer, got %q", key, v))
	}
	return n
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		panic(fmt.Sprintf("%s must be true or false, got %q", key, v))
	}
	return b
}
