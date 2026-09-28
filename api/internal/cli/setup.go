package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/selectdev/purros/api/internal/auth"
	"github.com/selectdev/purros/api/internal/events"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/modules/organization"
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
			return errors.New("setup has already been run (use `purros users sign-in-link --email …` to recover access)")
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
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, role_id, status) VALUES ($1, $2, $3, $4, 'invited')`,
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

// CreateLocation adds a location, records it in the audit log and emits
// location.created, like POST /locations.
func CreateLocation(ctx context.Context, pool *pgxpool.Pool, in LocationInput) (string, error) {
	if in.Name == "" {
		return "", errors.New("--name is required")
	}
	api := organization.LocationInput{Name: in.Name, Timezone: in.Timezone, Currency: in.Currency, BusinessDayCutoff: in.Cutoff}
	if in.Code != "" {
		api.Code = &in.Code
	}
	if in.ExternalID != "" {
		api.ExternalID = &in.ExternalID
	}
	var loc organization.Location
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) (err error) {
		if loc, err = organization.InsertLocation(ctx, tx, api); err != nil {
			return err
		}
		if err := events.Audit(ctx, tx, events.AuditEntry{
			Actor: cliActor, Action: "location.create", EntityType: "location", EntityID: loc.ID, After: loc,
		}); err != nil {
			return err
		}
		_, err = events.Emit(ctx, tx, events.Event{
			Type: "location.created", Feature: "core", LocationID: loc.ID,
			OrderingKey: "location:" + loc.ID, Actor: cliActor, Object: loc,
		})
		return err
	})
	return loc.ID, problemText(err)
}

// problemText turns an API problem into a plain CLI error ("timezone: Unknown time zone…").
func problemText(err error) error {
	p, ok := errors.AsType[*httpx.Problem](err)
	if !ok {
		return err
	}
	if len(p.Errors) == 0 {
		return errors.New(p.Error())
	}
	msgs := make([]string, len(p.Errors))
	for i, fe := range p.Errors {
		msgs[i] = fe.Path + ": " + fe.Message
	}
	return errors.New(strings.Join(msgs, "; "))
}

// SignInLink issues a single-use link for a user: an invitation for accounts
// that haven't signed in yet, otherwise a password reset. With resetMFA it
// also removes their authenticator app and recovery codes.
func SignInLink(ctx context.Context, pool *pgxpool.Pool, baseURL, email string, resetMFA bool) (string, error) {
	var userID, status string
	err := pool.QueryRow(ctx, `SELECT id, status FROM users WHERE lower(email) = lower($1)`, strings.TrimSpace(email)).Scan(&userID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("no account with email %q", email)
	}
	if err != nil {
		return "", err
	}
	if status == "deactivated" {
		return "", errors.New("this account is deactivated")
	}
	purpose, path, ttl := "password_reset", "/reset-password", time.Hour
	if status == "invited" {
		purpose, path, ttl = "invitation", "/invitation", 7*24*time.Hour
	}
	token, hash := auth.NewToken()
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM auth_tokens WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`, userID, purpose); err != nil {
			return err
		}
		if resetMFA {
			if _, err := tx.Exec(ctx, `UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL,
				failed_sign_ins = 0, locked_until = NULL WHERE id = $1`, userID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = $1`, userID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO auth_tokens (id, token_hash, user_id, purpose, expires_at) VALUES ($1, $2, $3, $4, $5)`,
			ids.New(ids.AuthToken), hash, userID, purpose, time.Now().Add(ttl))
		if err != nil {
			return err
		}
		return events.Audit(ctx, tx, events.AuditEntry{Actor: events.Actor{Type: "system", Name: "cli"}, Action: "user.sign_in_link_issued",
			EntityType: "user", EntityID: userID, After: map[string]any{"resetMfa": resetMFA}})
	})
	return strings.TrimRight(baseURL, "/") + path + "?token=" + token, err
}
