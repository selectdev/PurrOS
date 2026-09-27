// Package ingest runs batch ingestion endpoints: {"source": "...", "<records>": [...]}.
// Every record is validated on its own, runs in its own savepoint, and gets a
// result; the batch is logged in ingest_batches.
package ingest

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/modules/organization"
)

// Env is passed to the per-record function.
type Env struct {
	C       *httpx.Ctx
	Tx      pgx.Tx // savepoint for this record
	Source  string
	BatchID string
	Locs    *organization.LocationResolver
	Cache   map[string]any // per-batch cache for look-ups
}

// Outcome is what the per-record function returns.
type Outcome struct {
	Status   string // created | updated | duplicate
	ID       string
	Warnings []httpx.FieldError
}

// Reject aborts one record with a field error; the rest of the batch continues.
type Reject struct {
	Path, Message string
}

func (r *Reject) Error() string { return r.Path + ": " + r.Message }

func Rejectf(path, format string, args ...any) error {
	return &Reject{Path: path, Message: fmt.Sprintf(format, args...)}
}

// Run decodes the envelope, then calls fn for each record.
func Run[T any](c *httpx.Ctx, kind, field string, fn func(e *Env, i int, rec T) (Outcome, error)) (any, error) {
	var env map[string]json.RawMessage
	if err := c.Decode(&env); err != nil {
		return nil, err
	}
	var source string
	if raw, ok := env["source"]; ok {
		_ = json.Unmarshal(raw, &source)
	}
	if !httpx.ValidSource(source) {
		return nil, httpx.Validation(httpx.FieldError{Path: "source", Message: "Required: 1–100 characters without spaces or '/'"})
	}
	var records []json.RawMessage
	if raw, ok := env[field]; ok {
		if err := json.Unmarshal(raw, &records); err != nil {
			return nil, httpx.Validation(httpx.FieldError{Path: field, Message: "Must be an array"})
		}
	}
	if err := c.CheckBatchSize(field, len(records)); err != nil {
		return nil, err
	}

	res := httpx.BatchResult{BatchID: ids.New(ids.Batch), Received: len(records), Results: []httpx.RecordResult{}}
	err := c.InTx(func(tx pgx.Tx) error {
		e := &Env{C: c, Source: source, BatchID: res.BatchID, Locs: organization.NewLocationResolver(tx), Cache: map[string]any{}}
		for i, raw := range records {
			var rec T
			ext := externalID(raw)
			if err := json.Unmarshal(raw, &rec); err != nil {
				res.Add(httpx.Reject(i, ext, "", "Malformed record: "+err.Error()))
				continue
			}
			if errs := httpx.ValidateItem(rec); errs != nil {
				res.Add(httpx.RecordResult{Index: i, ExternalID: ext, Status: "rejected", Errors: errs})
				continue
			}
			var out Outcome
			err := httpx.Savepoint(c, tx, func(sp pgx.Tx) error {
				e.Tx = sp
				var err error
				out, err = fn(e, i, rec)
				return err
			})
			var rj *Reject
			var p *httpx.Problem
			switch {
			case errors.As(err, &rj):
				res.Add(httpx.Reject(i, ext, rj.Path, rj.Message))
			case errors.As(err, &p) && p.Status < 500:
				errs := p.Errors
				if len(errs) == 0 {
					errs = []httpx.FieldError{{Message: p.Detail}}
				}
				res.Add(httpx.RecordResult{Index: i, ExternalID: ext, Status: "rejected", Errors: errs})
			case err != nil:
				return err
			default:
				res.Add(httpx.RecordResult{Index: i, ExternalID: ext, Status: out.Status, ID: out.ID, Errors: out.Warnings})
			}
		}
		_, err := tx.Exec(c, `
			INSERT INTO ingest_batches (id, api_key_id, integration_id, kind, source, received, created, updated, rejected)
			VALUES ($1, $2, nullif($3, ''), $4, $5, $6, $7, $8, $9)`,
			res.BatchID, keyID(c), integrationID(c), kind, source, res.Received, res.Created, res.Updated, res.Rejected)
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// Status turns an upsert's "inserted" flag into a result status.
func Status(inserted bool) string {
	if inserted {
		return "created"
	}
	return "updated"
}

func externalID(raw json.RawMessage) string {
	var v struct {
		ExternalID string `json:"externalId"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.ExternalID
}

func keyID(c *httpx.Ctx) string {
	if c.Principal == nil {
		return ""
	}
	return c.Principal.KeyID
}

func integrationID(c *httpx.Ctx) string {
	if c.Principal == nil {
		return ""
	}
	return c.Principal.IntegrationID
}
