// Package inventory serves items. Stock, counts and transfers come later.
package inventory

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/shopspring/decimal"
)

const feature = "inventory"

type Item struct {
	ID          string           `json:"id"`
	SKU         string           `json:"sku"`
	Name        string           `json:"name"`
	Barcode     *string          `json:"barcode"`
	Category    *string          `json:"category"`
	BaseUnit    string           `json:"baseUnit"`
	ExternalID  *string          `json:"externalId"`
	DefaultCost *decimal.Decimal `json:"defaultCost"`
	TrackStock  bool             `json:"trackStock"`
	Version     int              `json:"version"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	ArchivedAt  *time.Time       `json:"archivedAt"`
}

type ItemInput struct {
	SKU         string           `json:"sku" validate:"required,max=100"`
	Name        string           `json:"name" validate:"required,max=200"`
	Barcode     *string          `json:"barcode,omitempty" validate:"omitempty,max=100"`
	Category    *string          `json:"category,omitempty" validate:"omitempty,max=100"`
	BaseUnit    string           `json:"baseUnit,omitempty" validate:"omitempty,max=20"`
	ExternalID  *string          `json:"externalId,omitempty" validate:"omitempty,extid"`
	DefaultCost *decimal.Decimal `json:"defaultCost,omitempty" doc:"Cost used before the first receipt sets an average cost"`
	TrackStock  *bool            `json:"trackStock,omitempty" doc:"Default true; false for services and non-stock items"`
}

const cols = `id, sku, name, barcode, category, base_unit, external_id, default_cost, track_stock, version, created_at, updated_at, archived_at`

func scan(r pgx.Row) (Item, error) {
	var i Item
	err := r.Scan(&i.ID, &i.SKU, &i.Name, &i.Barcode, &i.Category, &i.BaseUnit, &i.ExternalID, &i.DefaultCost, &i.TrackStock, &i.Version,
		&i.CreatedAt, &i.UpdatedAt, &i.ArchivedAt)
	return i, err
}

func get(ctx context.Context, q db.Querier, where string, arg any, lock bool) (Item, error) {
	sql := `SELECT ` + cols + ` FROM items WHERE ` + where
	if lock {
		sql += ` FOR UPDATE`
	}
	i, err := scan(q.QueryRow(ctx, sql, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return i, httpx.NotFound("Item not found.")
	}
	return i, err
}

func (i Item) toInput() ItemInput {
	return ItemInput{SKU: i.SKU, Name: i.Name, Barcode: i.Barcode, Category: i.Category, BaseUnit: i.BaseUnit, ExternalID: i.ExternalID,
		DefaultCost: i.DefaultCost, TrackStock: &i.TrackStock}
}

func conflict(err error) error {
	if db.IsUniqueViolation(err) {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "sku"):
			return httpx.Conflict("Another item already has this SKU.")
		case strings.Contains(msg, "barcode"):
			return httpx.Conflict("Another item already has this barcode.")
		case strings.Contains(msg, "external_id"):
			return httpx.Conflict("Another item already has this externalId.")
		}
	}
	return err
}

func insert(c *httpx.Ctx, tx pgx.Tx, in ItemInput) (Item, error) {
	if in.BaseUnit == "" {
		in.BaseUnit = "each"
	}
	track := in.TrackStock == nil || *in.TrackStock
	it, err := scan(tx.QueryRow(c, `
		INSERT INTO items (id, sku, name, barcode, category, base_unit, external_id, default_cost, track_stock)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+cols,
		ids.New(ids.Item), in.SKU, in.Name, in.Barcode, in.Category, in.BaseUnit, in.ExternalID, in.DefaultCost, track))
	if err != nil {
		return it, conflict(err)
	}
	return it, c.Record(tx, httpx.Change{Action: "item.create", EventType: "item.created", Feature: feature,
		EntityType: "item", EntityID: it.ID, After: it})
}

func update(c *httpx.Ctx, tx pgx.Tx, before Item, in ItemInput) (Item, error) {
	if in.BaseUnit == "" {
		in.BaseUnit = before.BaseUnit
	}
	track := before.TrackStock
	if in.TrackStock != nil {
		track = *in.TrackStock
	}
	after, err := scan(tx.QueryRow(c, `
		UPDATE items SET sku=$2, name=$3, barcode=$4, category=$5, base_unit=$6, external_id=$7, default_cost=$8, track_stock=$9,
			version=version+1, updated_at=now()
		WHERE id=$1 RETURNING `+cols,
		before.ID, in.SKU, in.Name, in.Barcode, in.Category, in.BaseUnit, in.ExternalID, in.DefaultCost, track))
	if err != nil {
		return after, conflict(err)
	}
	return after, c.Record(tx, httpx.Change{Action: "item.update", EventType: "item.updated", Feature: feature,
		EntityType: "item", EntityID: after.ID, Before: before, After: after})
}

type ListQuery struct {
	httpx.ListParams
	SKU     string `json:"sku,omitempty"`
	Barcode string `json:"barcode,omitempty"`
}

func Routes() []httpx.Route {
	return []httpx.Route{
		{
			Method: "GET", Path: "/items", Tag: "Inventory", Feature: feature, Scope: "inventory:read",
			Summary: "List items", Query: ListQuery{}, Response: httpx.Page[Item]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `SELECT `+cols+` FROM items
					WHERE id > $1 AND ($2::timestamptz IS NULL OR updated_at >= $2)
					  AND ($3 = '' OR sku = $3) AND ($4 = '' OR barcode = $4)
					ORDER BY id LIMIT $5`, lp.AfterID, lp.UpdatedSince, c.Query("sku"), c.Query("barcode"), lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) { return scan(r) })
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(i Item) string { return i.ID }), nil
			},
		},
		{
			Method: "POST", Path: "/items", Tag: "Inventory", Feature: feature, Scope: "inventory:write",
			Summary: "Create an item", Body: ItemInput{}, Response: Item{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in ItemInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Item
				err := c.InTx(func(tx pgx.Tx) (err error) { out, err = insert(c, tx, in); return err })
				return out, err
			},
		},
		{
			Method: "GET", Path: "/items/{id}", Tag: "Inventory", Feature: feature, Scope: "inventory:read",
			Summary: "Get an item", Response: Item{},
			Handler: func(c *httpx.Ctx) (any, error) { return get(c, c.App.Pool, "id = $1", c.Param("id"), false) },
		},
		{
			Method: "PATCH", Path: "/items/{id}", Tag: "Inventory", Feature: feature, Scope: "inventory:write",
			Summary: "Update an item (partial)", Body: ItemInput{}, Response: Item{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out Item
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := get(c, tx, "id = $1", c.Param("id"), true)
					if err != nil {
						return err
					}
					in, err := httpx.MergePatch(c, before.toInput())
					if err != nil {
						return err
					}
					out, err = update(c, tx, before, in)
					return err
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/items/external/{externalId}", Tag: "Inventory", Feature: feature, Scope: "inventory:read",
			Summary: "Get an item by external ID", Response: Item{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return get(c, c.App.Pool, "external_id = $1", c.Param("externalId"), false)
			},
		},
		{
			Method: "PUT", Path: "/items/external/{externalId}", Tag: "Inventory", Feature: feature, Scope: "inventory:write",
			Summary:     "Create or replace an item by external ID",
			Description: "Use this to sync a POS or online store catalog. Returns 201 when created, 200 when updated.",
			Body:        ItemInput{}, Response: Item{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in ItemInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				ext := c.Param("externalId")
				in.ExternalID = &ext
				var out Item
				created := false
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := get(c, tx, "external_id = $1", ext, true)
					var p *httpx.Problem
					if errors.As(err, &p) && p.Code == "not_found" {
						created = true
						out, err = insert(c, tx, in)
						return err
					}
					if err != nil {
						return err
					}
					out, err = update(c, tx, before, in)
					return err
				})
				if err != nil {
					return nil, err
				}
				if created {
					return httpx.Result{Status: 201, Body: out}, nil
				}
				return out, nil
			},
		},
	}
}
