// Package equipment serves the asset register, meter readings, preventive
// maintenance and repair work orders.
package equipment

import (
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/crud"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/shopspring/decimal"
)

const (
	feature = "equipment"
	tag     = "Equipment"
)

type Asset struct {
	ID                    string           `json:"id" db:"id"`
	LocationID            string           `json:"locationId" db:"location_id"`
	Name                  string           `json:"name" db:"name"`
	Category              *string          `json:"category" db:"category"`
	Make                  *string          `json:"make" db:"make"`
	Model                 *string          `json:"model" db:"model"`
	SerialNumber          *string          `json:"serialNumber" db:"serial_number"`
	PurchaseDate          *httpx.Date      `json:"purchaseDate" db:"purchase_date"`
	PurchaseCost          *decimal.Decimal `json:"purchaseCost" db:"purchase_cost"`
	WarrantyUntil         *httpx.Date      `json:"warrantyUntil" db:"warranty_until"`
	ServiceProvider       *string          `json:"serviceProvider" db:"service_provider"`
	MeterUnit             *string          `json:"meterUnit" db:"meter_unit"`
	MeterValue            *decimal.Decimal `json:"meterValue" db:"meter_value"`
	MaintenanceEveryDays  *int             `json:"maintenanceEveryDays" db:"maintenance_every_days"`
	MaintenanceEveryMeter *decimal.Decimal `json:"maintenanceEveryMeter" db:"maintenance_every_meter"`
	LastMaintainedAt      *time.Time       `json:"lastMaintainedAt" db:"last_maintained_at"`
	LastMaintainedMeter   *decimal.Decimal `json:"lastMaintainedMeter" db:"last_maintained_meter"`
	ExternalID            *string          `json:"externalId" db:"external_id"`
	Version               int              `json:"version" db:"version"`
	CreatedAt             time.Time        `json:"createdAt" db:"created_at"`
	UpdatedAt             time.Time        `json:"updatedAt" db:"updated_at"`
	ArchivedAt            *time.Time       `json:"archivedAt" db:"archived_at"`
}

type AssetInput struct {
	LocationID            string           `json:"locationId" db:"location_id" validate:"required"`
	Name                  string           `json:"name" db:"name" validate:"required,max=200"`
	Category              *string          `json:"category,omitempty" db:"category" validate:"omitempty,max=100"`
	Make                  *string          `json:"make,omitempty" db:"make" validate:"omitempty,max=100"`
	Model                 *string          `json:"model,omitempty" db:"model" validate:"omitempty,max=100"`
	SerialNumber          *string          `json:"serialNumber,omitempty" db:"serial_number" validate:"omitempty,max=100"`
	PurchaseDate          *httpx.Date      `json:"purchaseDate,omitempty" db:"purchase_date"`
	PurchaseCost          *decimal.Decimal `json:"purchaseCost,omitempty" db:"purchase_cost"`
	WarrantyUntil         *httpx.Date      `json:"warrantyUntil,omitempty" db:"warranty_until"`
	ServiceProvider       *string          `json:"serviceProvider,omitempty" db:"service_provider" validate:"omitempty,max=200"`
	MeterUnit             *string          `json:"meterUnit,omitempty" db:"meter_unit" validate:"omitempty,max=20" doc:"e.g. hours, km"`
	MaintenanceEveryDays  *int             `json:"maintenanceEveryDays,omitempty" db:"maintenance_every_days" validate:"omitempty,min=1"`
	MaintenanceEveryMeter *decimal.Decimal `json:"maintenanceEveryMeter,omitempty" db:"maintenance_every_meter"`
	ExternalID            *string          `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
}

var assets = &crud.Resource[Asset, AssetInput]{
	Path: "/assets", Table: "assets", Prefix: ids.Asset, Noun: "asset", Tag: tag,
	Feature: feature, ReadScope: "equipment:read", WriteScope: "equipment:write", External: true, Archive: true,
	Filters:    []crud.Filter{{Query: "locationId", Column: "location_id"}, {Query: "category", Column: "category"}},
	LocationOf: func(a *Asset) string { return a.LocationID },
}

type WorkOrder struct {
	ID          string           `json:"id" db:"id"`
	AssetID     *string          `json:"assetId" db:"asset_id"`
	LocationID  string           `json:"locationId" db:"location_id"`
	Title       string           `json:"title" db:"title"`
	Description *string          `json:"description" db:"description"`
	Kind        string           `json:"kind" db:"kind" doc:"repair or maintenance"`
	Priority    string           `json:"priority" db:"priority"`
	Status      string           `json:"status" db:"status"`
	Assignee    *string          `json:"assignee" db:"assignee" doc:"Technician or service provider"`
	Cost        *decimal.Decimal `json:"cost" db:"cost"`
	Photos      []string         `json:"photos" db:"photos"`
	ExternalID  *string          `json:"externalId" db:"external_id"`
	ResolvedAt  *time.Time       `json:"resolvedAt" db:"resolved_at"`
	ClosedAt    *time.Time       `json:"closedAt" db:"closed_at"`
	Version     int              `json:"version" db:"version"`
	CreatedAt   time.Time        `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time        `json:"updatedAt" db:"updated_at"`
}

type WorkOrderInput struct {
	AssetID     *string          `json:"assetId,omitempty" db:"asset_id"`
	LocationID  string           `json:"locationId,omitempty" db:"location_id" doc:"Defaults to the asset's location"`
	Title       string           `json:"title" db:"title" validate:"required,max=200"`
	Description *string          `json:"description,omitempty" db:"description" validate:"omitempty,max=4000"`
	Kind        string           `json:"kind,omitempty" db:"kind" validate:"omitempty,oneof=repair maintenance"`
	Priority    string           `json:"priority,omitempty" db:"priority" validate:"omitempty,oneof=low normal high urgent"`
	Status      string           `json:"status,omitempty" db:"status" validate:"omitempty,oneof=open assigned in_progress waiting_parts resolved closed"`
	Assignee    *string          `json:"assignee,omitempty" db:"assignee" validate:"omitempty,max=200"`
	Cost        *decimal.Decimal `json:"cost,omitempty" db:"cost"`
	Photos      []string         `json:"photos,omitempty" db:"photos" validate:"dive,url"`
	ExternalID  *string          `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
	ResolvedAt  *time.Time       `json:"-" db:"resolved_at"`
	ClosedAt    *time.Time       `json:"-" db:"closed_at"`
}

var workOrders = &crud.Resource[WorkOrder, WorkOrderInput]{
	Path: "/work-orders", Table: "work_orders", Prefix: ids.WorkOrder, Noun: "work_order", Tag: tag,
	Feature: "equipment.work_orders", ReadScope: "equipment:read", WriteScope: "equipment:write", External: true,
	CreatedEvent: "work_order.created", UpdatedEvent: "work_order.updated",
	Filters: []crud.Filter{{Query: "assetId", Column: "asset_id"}, {Query: "locationId", Column: "location_id"},
		{Query: "status", Column: "status"}, {Query: "kind", Column: "kind"}},
	LocationOf: func(w *WorkOrder) string { return w.LocationID },
	Check: func(c *httpx.Ctx, q db.Querier, in *WorkOrderInput, before *WorkOrder) error {
		if in.Photos == nil {
			in.Photos = []string{}
		}
		if in.Kind == "" {
			in.Kind = "repair"
		}
		if in.Priority == "" {
			in.Priority = "normal"
		}
		if in.Status == "" {
			in.Status = "open"
			if before != nil {
				in.Status = before.Status
			}
		}
		if in.AssetID != nil && in.LocationID == "" {
			a, err := assets.Get(c, q, "id", *in.AssetID, false)
			if err != nil {
				return httpx.Validation(httpx.FieldError{Path: "assetId", Message: "Unknown asset"})
			}
			in.LocationID = a.LocationID
		}
		if in.LocationID == "" {
			return httpx.Validation(httpx.FieldError{Path: "locationId", Message: "Required when there is no asset"})
		}
		// Keep or set lifecycle timestamps.
		if before != nil {
			in.ResolvedAt, in.ClosedAt = before.ResolvedAt, before.ClosedAt
		}
		now := time.Now()
		if (in.Status == "resolved" || in.Status == "closed") && in.ResolvedAt == nil {
			in.ResolvedAt = &now
		}
		if in.Status == "closed" && in.ClosedAt == nil {
			in.ClosedAt = &now
		}
		if in.Status != "closed" {
			in.ClosedAt = nil
		}
		if in.Status != "resolved" && in.Status != "closed" {
			in.ResolvedAt = nil
		}
		return nil
	},
	AfterWrite: func(c *httpx.Ctx, tx pgx.Tx, before, after *WorkOrder) error {
		if after.Status == "closed" && (before == nil || before.Status != "closed") {
			return c.Record(tx, httpx.Change{Action: "work_order.close", EventType: "work_order.closed", Feature: "equipment.work_orders",
				EntityType: "work_order", EntityID: after.ID, LocationID: after.LocationID, Before: before, After: after})
		}
		return nil
	},
}

type MeterInput struct {
	Value *decimal.Decimal `json:"value" validate:"required"`
	At    *time.Time       `json:"at,omitempty"`
}

type MaintainedInput struct {
	At          *time.Time       `json:"at,omitempty"`
	MeterValue  *decimal.Decimal `json:"meterValue,omitempty"`
	WorkOrderID string           `json:"workOrderId,omitempty" doc:"Close this maintenance work order"`
}

type DueAsset struct {
	Asset  Asset  `json:"asset"`
	Reason string `json:"reason"`
}

type dueList struct {
	Data []DueAsset `json:"data"`
}

// dueReason returns why an asset needs maintenance, or "".
func dueReason(a Asset, now time.Time) string {
	if a.MaintenanceEveryDays != nil {
		last := a.CreatedAt
		if a.LastMaintainedAt != nil {
			last = *a.LastMaintainedAt
		}
		if next := last.AddDate(0, 0, *a.MaintenanceEveryDays); !next.After(now) {
			return "Due by date since " + next.Format("2006-01-02")
		}
	}
	if a.MaintenanceEveryMeter != nil && a.MeterValue != nil {
		last := decimal.Zero
		if a.LastMaintainedMeter != nil {
			last = *a.LastMaintainedMeter
		}
		if a.MeterValue.Sub(last).GreaterThanOrEqual(*a.MaintenanceEveryMeter) {
			unit := ""
			if a.MeterUnit != nil {
				unit = " " + *a.MeterUnit
			}
			return "Due by meter: " + a.MeterValue.Sub(last).String() + unit + " since last maintenance"
		}
	}
	return ""
}

// raiseMaintenance emits maintenance.due and opens a maintenance work order
// unless one is already open.
func raiseMaintenance(c *httpx.Ctx, tx pgx.Tx, a Asset, reason string) error {
	var open bool
	if err := tx.QueryRow(c, `SELECT EXISTS (SELECT 1 FROM work_orders WHERE asset_id=$1 AND kind='maintenance' AND status NOT IN ('resolved','closed'))`, a.ID).Scan(&open); err != nil {
		return err
	}
	if open {
		return nil
	}
	if err := c.Record(tx, httpx.Change{Action: "maintenance.due", EventType: "maintenance.due", Feature: "equipment.maintenance",
		EntityType: "asset", EntityID: a.ID, LocationID: a.LocationID, After: map[string]any{"asset": a, "reason": reason}}); err != nil {
		return err
	}
	on, err := c.App.Features.IsEnabled(c, "equipment.work_orders")
	if err != nil || !on {
		return err
	}
	id := a.ID
	desc := reason
	_, err = workOrders.Insert(c, tx, WorkOrderInput{AssetID: &id, LocationID: a.LocationID, Title: "Scheduled maintenance: " + a.Name,
		Description: &desc, Kind: "maintenance"})
	return err
}

// Routes returns the equipment routes.
func Routes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "POST", Path: "/assets/{id}/meter-readings", Tag: tag, Feature: feature, Scope: "equipment:write",
			Summary:     "Record a meter reading (hours, km…)",
			Description: "When usage since the last maintenance reaches the asset's interval, maintenance.due is raised and a maintenance work order opened.",
			Body:        MeterInput{}, Response: Asset{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in MeterInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Asset
				err := c.InTx(func(tx pgx.Tx) error {
					a, err := assets.Get(c, tx, "id", c.Param("id"), true)
					if err != nil {
						return err
					}
					at := time.Now()
					if in.At != nil {
						at = *in.At
					}
					if _, err := tx.Exec(c, `INSERT INTO asset_meter_readings (asset_id, at, value) VALUES ($1,$2,$3)
						ON CONFLICT (asset_id, at) DO UPDATE SET value = EXCLUDED.value`, a.ID, at, *in.Value); err != nil {
						return err
					}
					if _, err := tx.Exec(c, `UPDATE assets SET meter_value=$2, updated_at=now() WHERE id=$1`, a.ID, *in.Value); err != nil {
						return err
					}
					out, err = assets.Get(c, tx, "id", a.ID, false)
					if err != nil {
						return err
					}
					if on, err := c.App.Features.IsEnabled(c, "equipment.maintenance"); err != nil {
						return err
					} else if on {
						if reason := dueReason(out, time.Now()); reason != "" {
							return raiseMaintenance(c, tx, out, reason)
						}
					}
					return nil
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/assets/{id}:maintained", Tag: tag, Feature: "equipment.maintenance", Scope: "equipment:write",
			Summary: "Record that maintenance was done", Body: MaintainedInput{}, Response: Asset{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in MaintainedInput
				if err := c.DecodeOptional(&in); err != nil {
					return nil, err
				}
				var out Asset
				err := c.InTx(func(tx pgx.Tx) error {
					a, err := assets.Get(c, tx, "id", c.Param("id"), true)
					if err != nil {
						return err
					}
					at := time.Now()
					if in.At != nil {
						at = *in.At
					}
					meter := a.MeterValue
					if in.MeterValue != nil {
						meter = in.MeterValue
					}
					if _, err := tx.Exec(c, `UPDATE assets SET last_maintained_at=$2, last_maintained_meter=$3, updated_at=now() WHERE id=$1`, a.ID, at, meter); err != nil {
						return err
					}
					if in.WorkOrderID != "" {
						wo, err := workOrders.Get(c, tx, "id", in.WorkOrderID, true)
						if err != nil {
							return err
						}
						next := WorkOrderInput{AssetID: wo.AssetID, LocationID: wo.LocationID, Title: wo.Title, Description: wo.Description,
							Kind: wo.Kind, Priority: wo.Priority, Status: "closed", Assignee: wo.Assignee, Cost: wo.Cost, Photos: wo.Photos, ExternalID: wo.ExternalID}
						if _, err := workOrders.Update(c, tx, wo, next); err != nil {
							return err
						}
					}
					out, err = assets.Get(c, tx, "id", a.ID, false)
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "asset.maintained", EntityType: "asset", EntityID: a.ID, LocationID: a.LocationID, Before: a, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/maintenance/due", Tag: tag, Feature: "equipment.maintenance", Scope: "equipment:read",
			Summary: "Assets due for maintenance by date or meter", Response: dueList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT id FROM assets WHERE archived_at IS NULL AND ($1 = '' OR location_id = $1)
					AND (maintenance_every_days IS NOT NULL OR maintenance_every_meter IS NOT NULL) ORDER BY id`, c.Query("locationId"))
				if err != nil {
					return nil, err
				}
				idList, err := pgx.CollectRows(rows, pgx.RowTo[string])
				if err != nil {
					return nil, err
				}
				out := dueList{Data: []DueAsset{}}
				for _, id := range idList {
					a, err := assets.Get(c, c.App.Pool, "id", id, false)
					if err != nil {
						return nil, err
					}
					if r := dueReason(a, time.Now()); r != "" {
						out.Data = append(out.Data, DueAsset{Asset: a, Reason: r})
					}
				}
				return out, nil
			},
		},
	}
	routes = append(routes, assets.Routes()...)
	routes = append(routes, workOrders.Routes()...)
	return routes
}
