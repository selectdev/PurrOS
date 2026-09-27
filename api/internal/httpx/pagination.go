package httpx

import (
	"encoding/base64"
	"strconv"
	"time"
)

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Page is the standard list response.
type Page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
}

// ListParams are the common list query parameters.
type ListParams struct {
	Limit        int        `json:"limit,omitempty" doc:"Page size, 1–200 (default 50)"`
	Cursor       string     `json:"cursor,omitempty" doc:"nextCursor from the previous page"`
	UpdatedSince *time.Time `json:"updatedSince,omitempty" doc:"Only records changed at or after this time"`

	AfterID string `json:"-"`
}

// ParseList reads limit, cursor and updatedSince. Lists are ordered by ID,
// which is creation order because IDs are time-sortable.
func (c *Ctx) ParseList() (ListParams, error) {
	p := ListParams{Limit: DefaultLimit}
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > MaxLimit {
			return p, Validation(FieldError{Path: "limit", Message: "Must be between 1 and 200"})
		}
		p.Limit = n
	}
	if s := c.Query("cursor"); s != "" {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil || len(b) == 0 {
			return p, Validation(FieldError{Path: "cursor", Message: "Invalid cursor"})
		}
		p.Cursor, p.AfterID = s, string(b)
	}
	if s := c.Query("updatedSince"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return p, Validation(FieldError{Path: "updatedSince", Message: "Must be an RFC 3339 timestamp"})
		}
		p.UpdatedSince = &t
	}
	return p, nil
}

// NewPage trims the extra row fetched to detect another page. Fetch limit+1
// rows ordered by ID, then pass them here with a function returning each ID.
func NewPage[T any](rows []T, limit int, id func(T) string) Page[T] {
	if rows == nil {
		rows = []T{}
	}
	if len(rows) <= limit {
		return Page[T]{Data: rows}
	}
	rows = rows[:limit]
	return Page[T]{
		Data:       rows,
		HasMore:    true,
		NextCursor: base64.RawURLEncoding.EncodeToString([]byte(id(rows[len(rows)-1]))),
	}
}
