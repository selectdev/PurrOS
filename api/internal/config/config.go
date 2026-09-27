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

	SMTP SMTP

	Backup Backup

	Storage Storage
}

// Storage configures where uploaded files live (STORAGE_*).
type Storage struct {
	Driver       string // STORAGE_DRIVER: local (default) or s3
	LocalPath    string // STORAGE_LOCAL_PATH (default /data/files)
	S3           S3     // STORAGE_S3_*
	SignedURLTTL int    // STORAGE_SIGNED_URL_TTL seconds (default 300)
	MaxUploadMB  int    // STORAGE_MAX_UPLOAD_MB (default 25)
}

// S3 is an S3-compatible bucket.
type S3 struct {
	Bucket          string
	Region          string
	Endpoint        string // empty for AWS
	AccessKeyID     string // empty: environment or instance role credentials
	SecretAccessKey string
	ForcePathStyle  bool
	Prefix          string
	SSE             string // AES256 or aws:kms
	KMSKeyID        string
}

func loadS3(prefix string) S3 {
	return S3{
		Bucket:          os.Getenv(prefix + "BUCKET"),
		Region:          os.Getenv(prefix + "REGION"),
		Endpoint:        os.Getenv(prefix + "ENDPOINT"),
		AccessKeyID:     os.Getenv(prefix + "ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv(prefix + "SECRET_ACCESS_KEY"),
		ForcePathStyle:  envBool(prefix+"FORCE_PATH_STYLE", false),
		Prefix:          os.Getenv(prefix + "PREFIX"),
		SSE:             os.Getenv(prefix + "SSE"),
		KMSKeyID:        os.Getenv(prefix + "KMS_KEY_ID"),
	}
}

// Backup configures scheduled backups. They are off when Dir is empty.
type Backup struct {
	Dir        string // PURROS_BACKUP_DIR
	HourUTC    int    // PURROS_BACKUP_HOUR (default 2)
	Keep       int    // PURROS_BACKUP_KEEP (default 14)
	Passphrase string // PURROS_BACKUP_PASSPHRASE: encrypt backups when set
	// Files says whether backups include uploaded files: auto (default:
	// only with local storage), true or false. PURROS_BACKUP_FILES
	Files string
	// S3 uploads every backup to a bucket (PURROS_BACKUP_S3_*).
	S3Enabled bool // PURROS_BACKUP_S3_ENABLED
	S3        S3
	S3Keep    int // PURROS_BACKUP_S3_KEEP (default PURROS_BACKUP_KEEP)
}

// Scheduled reports whether the worker makes daily backups.
func (b Backup) Scheduled() bool { return b.Dir != "" || b.S3Enabled }

// SMTP configures outgoing email. Email is off when Host is empty.
type SMTP struct {
	Host            string // SMTP_HOST
	Port            int    // SMTP_PORT (default 587)
	Secure          bool   // SMTP_SECURE: implicit TLS (port 465)
	User            string // SMTP_USER
	Password        string // SMTP_PASSWORD
	From            string // SMTP_FROM
	ReplyTo         string // SMTP_REPLY_TO
	RequireTLS      bool   // SMTP_REQUIRE_TLS (default true)
	TLSRejectUnauth bool   // SMTP_TLS_REJECT_UNAUTHORIZED (default true)
	RatePerSecond   int    // SMTP_RATE_PER_SECOND (default 10)
}

// Enabled reports whether email is configured.
func (s SMTP) Enabled() bool { return s.Host != "" }

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
		Backup: Backup{
			Dir:        os.Getenv("PURROS_BACKUP_DIR"),
			HourUTC:    envInt("PURROS_BACKUP_HOUR", 2),
			Keep:       envInt("PURROS_BACKUP_KEEP", 14),
			Passphrase: os.Getenv("PURROS_BACKUP_PASSPHRASE"),
			Files:      env("PURROS_BACKUP_FILES", "auto"),
			S3Enabled:  envBool("PURROS_BACKUP_S3_ENABLED", false),
			S3:         loadS3("PURROS_BACKUP_S3_"),
		},
		Storage: Storage{
			Driver:       env("STORAGE_DRIVER", "local"),
			LocalPath:    env("STORAGE_LOCAL_PATH", "/data/files"),
			S3:           loadS3("STORAGE_S3_"),
			SignedURLTTL: envInt("STORAGE_SIGNED_URL_TTL", 300),
			MaxUploadMB:  envInt("STORAGE_MAX_UPLOAD_MB", 25),
		},
		SMTP: SMTP{
			Host:            os.Getenv("SMTP_HOST"),
			Port:            envInt("SMTP_PORT", 587),
			Secure:          envBool("SMTP_SECURE", false),
			User:            os.Getenv("SMTP_USER"),
			Password:        os.Getenv("SMTP_PASSWORD"),
			From:            os.Getenv("SMTP_FROM"),
			ReplyTo:         os.Getenv("SMTP_REPLY_TO"),
			RequireTLS:      envBool("SMTP_REQUIRE_TLS", true),
			TLSRejectUnauth: envBool("SMTP_TLS_REJECT_UNAUTHORIZED", true),
			RatePerSecond:   envInt("SMTP_RATE_PER_SECOND", 10),
		},
	}
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	c.Backup.S3Keep = envInt("PURROS_BACKUP_S3_KEEP", c.Backup.Keep)
	if c.Backup.S3.Prefix == "" {
		c.Backup.S3.Prefix = "purros-backups/"
	}
	switch c.Storage.Driver {
	case "local":
	case "s3":
		if c.Storage.S3.Bucket == "" {
			errs = append(errs, errors.New("STORAGE_S3_BUCKET is required when STORAGE_DRIVER=s3"))
		}
	default:
		errs = append(errs, fmt.Errorf("STORAGE_DRIVER must be local or s3, got %q", c.Storage.Driver))
	}
	if c.Backup.S3Enabled && c.Backup.S3.Bucket == "" {
		errs = append(errs, errors.New("PURROS_BACKUP_S3_BUCKET is required when PURROS_BACKUP_S3_ENABLED=true"))
	}
	switch c.Backup.Files {
	case "auto", "true", "false":
	default:
		errs = append(errs, errors.New("PURROS_BACKUP_FILES must be auto, true or false"))
	}
	if c.Backup.HourUTC < 0 || c.Backup.HourUTC > 23 {
		errs = append(errs, errors.New("PURROS_BACKUP_HOUR must be between 0 and 23"))
	}
	if c.SMTP.Enabled() && c.SMTP.From == "" {
		errs = append(errs, errors.New("SMTP_FROM is required when SMTP_HOST is set"))
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

// IncludeFiles reports whether backups should contain uploaded files.
func (c Config) IncludeFiles() bool {
	switch c.Backup.Files {
	case "true":
		return true
	case "false":
		return false
	}
	return c.Storage.Driver == "local"
}
