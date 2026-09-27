package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/ids"
)

type SetupInput struct {
	Company    string
	Currency   string
	Timezone   string
	OwnerEmail string
	OwnerName  string
}

// Setup creates the company, the system roles and the first Owner. It only
// runs once.
func Setup(ctx context.Context, pool *pgxpool.Pool, in SetupInput) (ownerID string, err error) {
	if in.Company == "" || in.OwnerEmail == "" {
		return "", errors.New("--company and --owner-email are required")
	}
	if in.OwnerName == "" {
		in.OwnerName = in.OwnerEmail
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return "", fmt.Errorf("unknown timezone %q", in.Timezone)
	}
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM company)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errors.New("setup has already been run (use `purros owner:reset` to recover access)")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO company (id, name, currency, timezone) VALUES ($1, $2, $3, $4)`,
			ids.New(ids.Company), in.Company, in.Currency, in.Timezone); err != nil {
			return err
		}
		ownerRole, employeeRole := ids.New(ids.Role), ids.New(ids.Role)
		if _, err := tx.Exec(ctx, `
			INSERT INTO roles (id, name, description, system_key) VALUES
			($1, 'Owner', 'Every permission. Cannot be edited or deleted.', 'owner'),
			($2, 'Employee', 'Default role: access to the Employee Area only.', 'employee')`,
			ownerRole, employeeRole); err != nil {
			return err
		}
		ownerID = ids.New(ids.User)
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, role_id, status) VALUES ($1, $2, $3, $4, 'active')`,
			ownerID, in.OwnerEmail, in.OwnerName, ownerRole); err != nil {
			return err
		}
		return events.Audit(ctx, tx, events.AuditEntry{
			Actor: events.Actor{Type: "system", Name: "cli"}, Action: "company.setup",
			EntityType: "company", After: map[string]any{"company": in.Company, "owner": in.OwnerEmail},
		})
	})
	return ownerID, err
}

type LocationInput struct {
	Name       string
	Code       string
	ExternalID string
	Timezone   string
	Currency   string
	Cutoff     string // "HH:MM"
}

// CreateLocation adds a location (the web UI will do this later).
func CreateLocation(ctx context.Context, pool *pgxpool.Pool, in LocationInput) (string, error) {
	if in.Name == "" {
		return "", errors.New("--name is required")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return "", fmt.Errorf("unknown timezone %q", in.Timezone)
	}
	cutoff, err := time.Parse("15:04", in.Cutoff)
	if err != nil {
		return "", fmt.Errorf("--cutoff must be HH:MM")
	}
	id := ids.New(ids.Location)
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO locations (id, name, code, external_id, timezone, currency, business_day_cutoff)
			VALUES ($1, $2, nullif($3, ''), nullif($4, ''), $5, $6, make_interval(hours => $7, mins => $8))`,
			id, in.Name, in.Code, in.ExternalID, in.Timezone, in.Currency, cutoff.Hour(), cutoff.Minute())
		if err != nil {
			return err
		}
		return events.Audit(ctx, tx, events.AuditEntry{
			Actor: events.Actor{Type: "system", Name: "cli"}, Action: "location.create",
			EntityType: "location", EntityID: id, After: map[string]any{"name": in.Name, "externalId": in.ExternalID},
		})
	})
	return id, err
}
