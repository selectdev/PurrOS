package httpx

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RecordResult is the outcome for one record of a batch.
type RecordResult struct {
	Index      int          `json:"index"`
	ExternalID string       `json:"externalId,omitempty"`
	Status     string       `json:"status" doc:"created, updated, unchanged, duplicate or rejected"`
	ID         string       `json:"id,omitempty"`
	Errors     []FieldError `json:"errors,omitempty"`
}

// BatchResult is returned by batch ingestion endpoints with status 202.
type BatchResult struct {
	BatchID  string         `json:"batchId"`
	Received int            `json:"received"`
	Created  int            `json:"created"`
	Updated  int            `json:"updated"`
	Rejected int            `json:"rejected"`
	Results  []RecordResult `json:"results"`
}

func (b *BatchResult) Add(r RecordResult) {
	switch r.Status {
	case "created":
		b.Created++
	case "updated":
		b.Updated++
	case "rejected":
		b.Rejected++
	}
	b.Results = append(b.Results, r)
}

// ValidateItem validates one batch record and returns its field errors
// (paths relative to the record), or nil.
func ValidateItem(v any) []FieldError {
	err := Validate(v)
	if err == nil {
		return nil
	}
	if p, ok := errors.AsType[*Problem](err); ok {
		if len(p.Errors) > 0 {
			return p.Errors
		}
		return []FieldError{{Path: "", Message: p.Detail}}
	}
	return []FieldError{{Message: err.Error()}}
}

// Reject builds a rejected result with one error.
func Reject(index int, externalID, path, msg string) RecordResult {
	return RecordResult{Index: index, ExternalID: externalID, Status: "rejected",
		Errors: []FieldError{{Path: path, Message: msg}}}
}

// Savepoint runs fn inside a nested transaction (savepoint) so a failing
// record rolls back only its own changes.
func Savepoint(c *Ctx, tx pgx.Tx, fn func(sp pgx.Tx) error) error {
	sp, err := tx.Begin(c)
	if err != nil {
		return err
	}
	if err := fn(sp); err != nil {
		_ = sp.Rollback(c)
		return err
	}
	return sp.Commit(c)
}

// CheckBatchSize validates the number of records against API_MAX_BATCH_SIZE.
func (c *Ctx) CheckBatchSize(field string, n int) error {
	if n == 0 {
		return Validation(FieldError{Path: field, Message: "At least 1 record"})
	}
	if max := c.App.Config.MaxBatchSize; n > max {
		return Validation(FieldError{Path: field, Message: "At most " + itoa(max) + " records per batch"})
	}
	return nil
}

// ValidSource checks a data source label such as "pos:store-101".
func ValidSource(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	return !strings.ContainsAny(s, " \t\n/")
}
