package httpx

import (
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const idempotencyTTL = 24 * time.Hour

type replay struct {
	status int
	body   []byte
}

func requestHash(c *Ctx) []byte {
	h := sha256.New()
	h.Write([]byte(c.Req.Method))
	h.Write([]byte{0})
	h.Write([]byte(c.Req.URL.Path))
	h.Write([]byte{0})
	h.Write(c.body)
	return h.Sum(nil)
}

func (a *App) idempotencyLookup(c *Ctx, key string) (*replay, *Problem, error) {
	var (
		hash    []byte
		status  int
		body    []byte
		created time.Time
	)
	err := a.Pool.QueryRow(c, `
		SELECT request_hash, status_code, response_body, created_at
		FROM idempotency_records WHERE api_key_id = $1 AND key = $2`,
		c.Principal.KeyID, key).Scan(&hash, &status, &body, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if a.Now().Sub(created) > idempotencyTTL {
		_, err := a.Pool.Exec(c, `DELETE FROM idempotency_records WHERE api_key_id = $1 AND key = $2`,
			c.Principal.KeyID, key)
		return nil, nil, err
	}
	if string(hash) != string(requestHash(c)) {
		return nil, Conflict("This Idempotency-Key was already used with a different request."), nil
	}
	return &replay{status: status, body: body}, nil, nil
}

func (a *App) idempotencyStore(c *Ctx, key string, status int, body []byte) error {
	if body == nil {
		body = []byte{}
	}
	_, err := a.Pool.Exec(c, `
		INSERT INTO idempotency_records (api_key_id, key, method, path, request_hash, status_code, response_body)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (api_key_id, key) DO NOTHING`,
		c.Principal.KeyID, key, c.Req.Method, c.Req.URL.Path, requestHash(c), status, body)
	return err
}
