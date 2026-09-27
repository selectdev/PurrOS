// Package refs resolves references that clients may send in several forms:
// employees by ID or external ID, items by ID, external ID, SKU or barcode.
package refs

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
)

// ItemRef identifies an item. Set one field.
type ItemRef struct {
	ItemID         string `json:"itemId,omitempty"`
	ItemSKU        string `json:"itemSku,omitempty"`
	ItemExternalID string `json:"itemExternalId,omitempty"`
	ItemBarcode    string `json:"itemBarcode,omitempty"`
}

func (r ItemRef) empty() bool {
	return r.ItemID == "" && r.ItemSKU == "" && r.ItemExternalID == "" && r.ItemBarcode == ""
}

// Item resolves an item reference. path is used in the error (e.g. "lines[2]").
func Item(ctx context.Context, q db.Querier, r ItemRef, path string) (string, error) {
	if r.empty() {
		return "", httpx.Validation(httpx.FieldError{Path: join(path, "itemId"), Message: "Set itemId, itemSku, itemExternalId or itemBarcode"})
	}
	var id string
	err := q.QueryRow(ctx, `
		SELECT id FROM items
		WHERE ($1 <> '' AND id = $1) OR ($2 <> '' AND sku = $2) OR ($3 <> '' AND external_id = $3) OR ($4 <> '' AND barcode = $4)
		LIMIT 1`, r.ItemID, r.ItemSKU, r.ItemExternalID, r.ItemBarcode).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", httpx.Validation(httpx.FieldError{Path: join(path, "itemId"), Message: "Unknown item"})
	}
	return id, err
}

// EmployeeRef identifies an employee.
type EmployeeRef struct {
	EmployeeID         string `json:"employeeId,omitempty"`
	EmployeeExternalID string `json:"employeeExternalId,omitempty"`
}

// Employee resolves an employee reference. It returns "" without error when
// the reference is empty and optional is true.
func Employee(ctx context.Context, q db.Querier, r EmployeeRef, path string, optional bool) (string, error) {
	if r.EmployeeID == "" && r.EmployeeExternalID == "" {
		if optional {
			return "", nil
		}
		return "", httpx.Validation(httpx.FieldError{Path: join(path, "employeeId"), Message: "Set employeeId or employeeExternalId"})
	}
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM employees WHERE ($1 <> '' AND id = $1) OR ($2 <> '' AND external_id = $2) LIMIT 1`,
		r.EmployeeID, r.EmployeeExternalID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", httpx.Validation(httpx.FieldError{Path: join(path, "employeeId"), Message: "Unknown employee"})
	}
	return id, err
}

// LocationRef identifies a location.
type LocationRef struct {
	LocationID         string `json:"locationId,omitempty"`
	LocationExternalID string `json:"locationExternalId,omitempty"`
}

// Location resolves a location reference to its ID.
func Location(ctx context.Context, q db.Querier, r LocationRef, path string) (string, error) {
	if r.LocationID == "" && r.LocationExternalID == "" {
		return "", httpx.Validation(httpx.FieldError{Path: join(path, "locationId"), Message: "Set locationId or locationExternalId"})
	}
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM locations WHERE ($1 <> '' AND id = $1) OR ($2 <> '' AND external_id = $2) LIMIT 1`,
		r.LocationID, r.LocationExternalID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", httpx.Validation(httpx.FieldError{Path: join(path, "locationId"), Message: "Unknown location"})
	}
	return id, err
}

func join(prefix, field string) string {
	if prefix == "" {
		return field
	}
	return fmt.Sprintf("%s.%s", prefix, field)
}
