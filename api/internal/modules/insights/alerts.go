package insights

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/crud"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/shopspring/decimal"
)

type AlertRule struct {
	ID              string          `json:"id" db:"id"`
	Name            string          `json:"name" db:"name"`
	KPI             string          `json:"kpi" db:"kpi"`
	LocationID      *string         `json:"locationId" db:"location_id" doc:"null watches all locations combined"`
	Comparator      string          `json:"comparator" db:"comparator" doc:"gt (above) or lt (below)"`
	Threshold       decimal.Decimal `json:"threshold" db:"threshold"`
	LastTriggeredOn *httpx.Date     `json:"lastTriggeredOn" db:"last_triggered_on"`
	CreatedAt       time.Time       `json:"createdAt" db:"created_at"`
	UpdatedAt       time.Time       `json:"updatedAt" db:"updated_at"`
	ArchivedAt      *time.Time      `json:"archivedAt" db:"archived_at"`
}

type AlertRuleInput struct {
	Name       string           `json:"name,omitempty" db:"name" validate:"max=200"`
	KPI        string           `json:"kpi" db:"kpi" validate:"required" doc:"A KPI key from GET /kpis, e.g. laborPercent"`
	LocationID *string          `json:"locationId,omitempty" db:"location_id"`
	Comparator string           `json:"comparator" db:"comparator" validate:"required,oneof=gt lt"`
	Threshold  *decimal.Decimal `json:"threshold" db:"threshold" validate:"required"`
}

var alertRules = &crud.Resource[AlertRule, AlertRuleInput]{
	Path: "/alert-rules", Table: "alert_rules", Prefix: ids.AlertRule, Noun: "alert_rule", Tag: tag,
	Feature: feature, ReadScope: "reports:read", WriteScope: "reports:write", Archive: true,
	Filters: []crud.Filter{{Query: "kpi", Column: "kpi"}, {Query: "locationId", Column: "location_id"}},
	Check: func(c *httpx.Ctx, q db.Querier, in *AlertRuleInput, before *AlertRule) error {
		if !slices.Contains(KPIKeys, in.KPI) {
			return httpx.Validation(httpx.FieldError{Path: "kpi", Message: "Must be one of: " + strings.Join(KPIKeys, ", ")})
		}
		if in.Name == "" {
			op := map[string]string{"gt": "above", "lt": "below"}[in.Comparator]
			in.Name = fmt.Sprintf("%s %s %s", in.KPI, op, in.Threshold.String())
		}
		return nil
	},
	LocationOf: func(r *AlertRule) string {
		if r.LocationID == nil {
			return ""
		}
		return *r.LocationID
	},
}

// Triggered is the payload of an alert.triggered event.
type Triggered struct {
	Rule  AlertRule       `json:"rule"`
	Date  httpx.Date      `json:"date"`
	Value decimal.Decimal `json:"value"`
}

// EvaluateAlerts checks every active alert rule against today's KPIs (in the
// rule location's time zone, or UTC for all locations) and raises
// alert.triggered at most once per rule per day. It returns how many fired.
func EvaluateAlerts(ctx context.Context, pool *pgxpool.Pool, on Enabled, now time.Time) (int, error) {
	if ok, err := on(ctx, feature); err != nil || !ok {
		return 0, err
	}
	rows, err := pool.Query(ctx, `SELECT r.id, coalesce(l.timezone, 'UTC') FROM alert_rules r
		LEFT JOIN locations l ON l.id = r.location_id WHERE r.archived_at IS NULL ORDER BY r.id`)
	if err != nil {
		return 0, err
	}
	type pending struct{ id, tz string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pending, error) {
		var p pending
		return p, r.Scan(&p.id, &p.tz)
	})
	if err != nil {
		return 0, err
	}
	fired := 0
	for _, p := range list {
		tz, err := time.LoadLocation(p.tz)
		if err != nil {
			tz = time.UTC
		}
		today := httpx.NewDate(now.In(tz))
		ok, err := evaluateOne(ctx, pool, on, p.id, today)
		if err != nil {
			return fired, fmt.Errorf("alert rule %s: %w", p.id, err)
		}
		if ok {
			fired++
		}
	}
	return fired, nil
}

func evaluateOne(ctx context.Context, pool *pgxpool.Pool, on Enabled, id string, today httpx.Date) (bool, error) {
	fired := false
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		rule, err := alertRules.Get(ctx, tx, "id", id, true)
		var notFound *httpx.Problem
		if errors.As(err, &notFound) {
			return nil // deleted meanwhile
		}
		if err != nil || rule.ArchivedAt != nil {
			return err
		}
		if rule.LastTriggeredOn != nil && rule.LastTriggeredOn.Equal(today.Time) {
			return nil
		}
		loc := ""
		if rule.LocationID != nil {
			loc = *rule.LocationID
		}
		k, err := Compute(ctx, tx, on, today.Time, today.Time, loc)
		if err != nil {
			return err
		}
		v := k.Value(rule.KPI)
		if v == nil {
			return nil
		}
		hit := (rule.Comparator == "gt" && v.GreaterThan(rule.Threshold)) || (rule.Comparator == "lt" && v.LessThan(rule.Threshold))
		if !hit {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE alert_rules SET last_triggered_on = $2 WHERE id = $1`, rule.ID, today); err != nil {
			return err
		}
		rule.LastTriggeredOn = &today
		fired = true
		_, err = events.Emit(ctx, tx, events.Event{
			Type: "alert.triggered", Feature: feature, LocationID: loc, OrderingKey: "alert_rule:" + rule.ID,
			Actor: events.System, Object: Triggered{Rule: rule, Date: today, Value: *v},
		})
		return err
	})
	return fired, err
}
