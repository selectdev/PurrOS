// Package crud generates the standard routes for simple resources: list, get,
// create, partial update, get/upsert by external ID and archive. Columns come
// from `db` struct tags, so a resource is declared once and gets the same
// auth, feature checks, validation, audit log and events as hand-written code.
package crud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
)

// Filter maps a query parameter to an equality condition on a column.
type Filter struct {
	Query  string // query parameter name
	Column string
	Doc    string
}

// Resource describes a table-backed resource. Out is the response type; In is
// the writable subset. Both use `db` tags for columns and `json` tags for the API.
type Resource[Out any, In any] struct {
	Path       string // e.g. "/suppliers"
	Table      string
	Prefix     string // ID prefix
	Noun       string // e.g. "supplier" (audit actions "supplier.create")
	Tag        string // OpenAPI tag
	Feature    string
	ReadScope  string
	WriteScope string

	// Nested resources: e.g. Path "/employees/{employeeId}/documents" with
	// ParentParam "employeeId" and ParentColumn "employee_id". In must have a
	// field for the parent column tagged json:"-".
	ParentParam, ParentColumn string

	External bool // externalId lookup and upsert routes (needs external_id column)
	NoList   bool // omit the generic list route (the module provides its own)
	Archive  bool // DELETE archives (needs archived_at column)
	NoCreate bool // omit POST (e.g. resources created elsewhere)
	NoUpdate bool // omit PATCH

	// Events to emit; leave empty to only audit.
	CreatedEvent, UpdatedEvent, ArchivedEvent string

	Filters []Filter

	// Defaults fills in defaults before insert.
	Defaults func(in *In)
	// Check runs inside the transaction before insert (before == nil) or update.
	Check func(c *httpx.Ctx, q db.Querier, in *In, before *Out) error
	// LocationOf returns the location to attach to events.
	LocationOf func(o *Out) string

	outCols, inCols []column
	hasVersion      bool
	hasUpdatedAt    bool
}

type column struct {
	name  string // db column
	json  string // json name
	field []int  // field index path
}

func columns(t reflect.Type) []column {
	var out []column
	var walk func(t reflect.Type, idx []int)
	walk = func(t reflect.Type, idx []int) {
		for i := range t.NumField() {
			f := t.Field(i)
			path := append(append([]int{}, idx...), i)
			if f.Anonymous && f.Type.Kind() == reflect.Struct {
				walk(f.Type, path)
				continue
			}
			name := f.Tag.Get("db")
			if name == "" || name == "-" {
				continue
			}
			j, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			out = append(out, column{name: name, json: j, field: path})
		}
	}
	walk(t, nil)
	return out
}

func (r *Resource[Out, In]) init() {
	if r.outCols != nil {
		return
	}
	r.outCols = columns(reflect.TypeOf(*new(Out)))
	r.inCols = columns(reflect.TypeOf(*new(In)))
	for _, c := range r.outCols {
		switch c.name {
		case "version":
			r.hasVersion = true
		case "updated_at":
			r.hasUpdatedAt = true
		}
	}
}

func (r *Resource[Out, In]) selectList() string {
	names := make([]string, len(r.outCols))
	for i, c := range r.outCols {
		names[i] = c.name
	}
	return strings.Join(names, ", ")
}

func (r *Resource[Out, In]) scan(row pgx.Row) (Out, error) {
	var o Out
	v := reflect.ValueOf(&o).Elem()
	dest := make([]any, len(r.outCols))
	for i, c := range r.outCols {
		dest[i] = v.FieldByIndex(c.field).Addr().Interface()
	}
	return o, row.Scan(dest...)
}

func (r *Resource[Out, In]) inValues(in *In) []any {
	v := reflect.ValueOf(in).Elem()
	vals := make([]any, len(r.inCols))
	for i, c := range r.inCols {
		vals[i] = v.FieldByIndex(c.field).Interface()
	}
	return vals
}

// Get loads one record by a column value.
func (r *Resource[Out, In]) Get(ctx context.Context, q db.Querier, col string, val any, lock bool) (Out, error) {
	r.init()
	sql := `SELECT ` + r.selectList() + ` FROM ` + r.Table + ` WHERE ` + col + ` = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	o, err := r.scan(q.QueryRow(ctx, sql, val))
	if errors.Is(err, pgx.ErrNoRows) {
		return o, httpx.NotFound(strings.ToUpper(r.Noun[:1]) + strings.ReplaceAll(r.Noun[1:], "_", " ") + " not found.")
	}
	return o, err
}

// Insert creates a record inside tx and records the change.
func (r *Resource[Out, In]) Insert(c *httpx.Ctx, tx pgx.Tx, in In) (Out, error) {
	r.init()
	if r.Defaults != nil {
		r.Defaults(&in)
	}
	if r.ParentParam != "" {
		r.setParent(&in, c.Param(r.ParentParam))
	}
	if r.Check != nil {
		if err := r.Check(c, tx, &in, nil); err != nil {
			return *new(Out), err
		}
	}
	cols := []string{"id"}
	ph := []string{"$1"}
	args := []any{ids.New(r.Prefix)}
	for i, col := range r.inCols {
		cols = append(cols, col.name)
		ph = append(ph, fmt.Sprintf("$%d", i+2))
	}
	args = append(args, r.inValues(&in)...)
	out, err := r.scan(tx.QueryRow(c, `INSERT INTO `+r.Table+` (`+strings.Join(cols, ", ")+`) VALUES (`+
		strings.Join(ph, ", ")+`) RETURNING `+r.selectList(), args...))
	if err != nil {
		return out, r.mapError(err)
	}
	return out, r.record(c, tx, "create", r.CreatedEvent, nil, &out)
}

// Update replaces the writable columns of before inside tx.
func (r *Resource[Out, In]) Update(c *httpx.Ctx, tx pgx.Tx, before Out, in In) (Out, error) {
	r.init()
	if r.Check != nil {
		if err := r.Check(c, tx, &in, &before); err != nil {
			return before, err
		}
	}
	sets := make([]string, 0, len(r.inCols)+2)
	for i, col := range r.inCols {
		if col.name == r.ParentColumn {
			continue // the parent never changes
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", col.name, i+2))
	}
	if r.hasVersion {
		sets = append(sets, "version = version + 1")
	}
	if r.hasUpdatedAt {
		sets = append(sets, "updated_at = now()")
	}
	if r.ParentParam != "" {
		r.setParent(&in, parentOf(before, r))
	}
	args := append([]any{idOf(before)}, r.inValues(&in)...)
	out, err := r.scan(tx.QueryRow(c, `UPDATE `+r.Table+` SET `+strings.Join(sets, ", ")+
		` WHERE id = $1 RETURNING `+r.selectList(), args...))
	if err != nil {
		return out, r.mapError(err)
	}
	return out, r.record(c, tx, "update", r.UpdatedEvent, &before, &out)
}

func (r *Resource[Out, In]) record(c *httpx.Ctx, tx pgx.Tx, action, event string, before, after *Out) error {
	ch := httpx.Change{
		Action: r.Noun + "." + action, EventType: event, Feature: r.Feature,
		EntityType: r.Noun, EntityID: idOf(*after), After: *after,
	}
	if before != nil {
		ch.Before = *before
	}
	if r.LocationOf != nil {
		ch.LocationID = r.LocationOf(after)
	}
	return c.Record(tx, ch)
}

func (r *Resource[Out, In]) setParent(in *In, val string) {
	v := reflect.ValueOf(in).Elem()
	for _, c := range r.inCols {
		if c.name == r.ParentColumn {
			f := v.FieldByIndex(c.field)
			if f.Kind() == reflect.Pointer {
				f.Set(reflect.ValueOf(&val))
			} else {
				f.SetString(val)
			}
		}
	}
}

func parentOf[Out, In any](o Out, r *Resource[Out, In]) string {
	v := reflect.ValueOf(o)
	for _, c := range r.outCols {
		if c.name == r.ParentColumn {
			f := v.FieldByIndex(c.field)
			if f.Kind() == reflect.Pointer {
				if f.IsNil() {
					return ""
				}
				return f.Elem().String()
			}
			return f.String()
		}
	}
	return ""
}

// getScoped loads a record by ID, also matching the parent from the path.
func (r *Resource[Out, In]) getScoped(c *httpx.Ctx, q db.Querier, lock bool) (Out, error) {
	o, err := r.Get(c, q, "id", c.Param("id"), lock)
	if err == nil && r.ParentParam != "" && parentOf(o, r) != c.Param(r.ParentParam) {
		return o, httpx.NotFound(strings.ToUpper(r.Noun[:1]) + strings.ReplaceAll(r.Noun[1:], "_", " ") + " not found.")
	}
	return o, err
}

func idOf(v any) string {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	return rv.FieldByName("ID").String()
}

func toInput[Out, In any](o Out) In {
	var in In
	b, _ := json.Marshal(o)
	_ = json.Unmarshal(b, &in)
	return in
}

// mapError turns constraint violations into API errors naming the JSON field.
func (r *Resource[Out, In]) mapError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	field := r.fieldForConstraint(pgErr.ConstraintName)
	switch pgErr.Code {
	case "23505":
		if field != "" {
			return httpx.Conflict(fmt.Sprintf("Another %s already has this %s.", strings.ReplaceAll(r.Noun, "_", " "), field))
		}
		return httpx.Conflict("A record with these values already exists.")
	case "23503":
		if field == "" {
			field = "reference"
		}
		return httpx.Validation(httpx.FieldError{Path: field, Message: "Unknown or in-use reference"})
	case "23514":
		return httpx.Validation(httpx.FieldError{Path: field, Message: "Invalid value (" + pgErr.ConstraintName + ")"})
	}
	return err
}

func (r *Resource[Out, In]) fieldForConstraint(name string) string {
	best := ""
	for _, c := range r.inCols {
		if strings.Contains(name, "_"+c.name+"_") || strings.HasSuffix(name, "_"+c.name) {
			if len(c.name) > len(best) {
				best = c.json
			}
		}
	}
	return best
}

type listQuery struct {
	httpx.ListParams
}

func ifMatch(c *httpx.Ctx, version int) error {
	h := strings.Trim(c.Req.Header.Get("If-Match"), `"`)
	if h == "" {
		return nil
	}
	if h != fmt.Sprint(version) {
		return httpx.VersionMismatch()
	}
	return nil
}

func versionOf(v any) (int, bool) {
	f := reflect.ValueOf(v).FieldByName("Version")
	if !f.IsValid() {
		return 0, false
	}
	return int(f.Int()), true
}

// Routes returns the resource's routes.
func (r *Resource[Out, In]) Routes() []httpx.Route {
	r.init()
	var routes []httpx.Route
	name := strings.ReplaceAll(r.Noun, "_", " ")
	listDesc := ""
	for _, f := range r.Filters {
		listDesc += fmt.Sprintf("`%s` filters by %s. ", f.Query, strings.ReplaceAll(f.Column, "_", " "))
	}
	if !r.NoList {
		routes = append(routes, httpx.Route{
			Method: "GET", Path: r.Path, Tag: r.Tag, Feature: r.Feature, Scope: r.ReadScope,
			Summary: "List " + name + "s", Description: strings.TrimSpace(listDesc),
			Query: listQuery{}, Response: httpx.Page[Out]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				where := []string{"id > $1"}
				args := []any{lp.AfterID}
				if r.ParentParam != "" {
					args = append(args, c.Param(r.ParentParam))
					where = append(where, fmt.Sprintf("%s = $%d", r.ParentColumn, len(args)))
				}
				if lp.UpdatedSince != nil && r.hasUpdatedAt {
					args = append(args, *lp.UpdatedSince)
					where = append(where, fmt.Sprintf("updated_at >= $%d", len(args)))
				}
				for _, f := range r.Filters {
					if v := c.Query(f.Query); v != "" {
						args = append(args, v)
						where = append(where, fmt.Sprintf("%s::text = $%d", f.Column, len(args)))
					}
				}
				if r.Archive && c.Query("includeArchived") != "true" {
					where = append(where, "archived_at IS NULL")
				}
				args = append(args, lp.Limit+1)
				rows, err := c.App.Pool.Query(c, `SELECT `+r.selectList()+` FROM `+r.Table+` WHERE `+
					strings.Join(where, " AND ")+fmt.Sprintf(` ORDER BY id LIMIT $%d`, len(args)), args...)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Out, error) { return r.scan(row) })
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(o Out) string { return idOf(o) }), nil
			},
		})
	}
	routes = append(routes, httpx.Route{
		Method: "GET", Path: r.Path + "/{id}", Tag: r.Tag, Feature: r.Feature, Scope: r.ReadScope,
		Summary: "Get a " + name, Response: *new(Out),
		Handler: func(c *httpx.Ctx) (any, error) { return r.getScoped(c, c.App.Pool, false) },
	})
	if !r.NoCreate {
		routes = append(routes, httpx.Route{
			Method: "POST", Path: r.Path, Tag: r.Tag, Feature: r.Feature, Scope: r.WriteScope,
			Summary: "Create a " + name, Body: *new(In), Response: *new(Out), Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in In
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Out
				err := c.InTx(func(tx pgx.Tx) (err error) { out, err = r.Insert(c, tx, in); return err })
				return out, err
			},
		})
	}
	if !r.NoUpdate {
		routes = append(routes, httpx.Route{
			Method: "PATCH", Path: r.Path + "/{id}", Tag: r.Tag, Feature: r.Feature, Scope: r.WriteScope,
			Summary: "Update a " + name + " (partial)", Body: *new(In), Response: *new(Out),
			Handler: func(c *httpx.Ctx) (any, error) {
				var out Out
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := r.getScoped(c, tx, true)
					if err != nil {
						return err
					}
					if v, ok := versionOf(before); ok {
						if err := ifMatch(c, v); err != nil {
							return err
						}
					}
					in, err := httpx.MergePatch(c, toInput[Out, In](before))
					if err != nil {
						return err
					}
					out, err = r.Update(c, tx, before, in)
					return err
				})
				return out, err
			},
		})
	}
	if r.External {
		routes = append(routes,
			httpx.Route{
				Method: "GET", Path: r.Path + "/external/{externalId}", Tag: r.Tag, Feature: r.Feature, Scope: r.ReadScope,
				Summary: "Get a " + name + " by external ID", Response: *new(Out),
				Handler: func(c *httpx.Ctx) (any, error) {
					return r.Get(c, c.App.Pool, "external_id", c.Param("externalId"), false)
				},
			},
			httpx.Route{
				Method: "PUT", Path: r.Path + "/external/{externalId}", Tag: r.Tag, Feature: r.Feature, Scope: r.WriteScope,
				Summary:     "Create or replace a " + name + " by external ID",
				Description: "Returns 201 when created and 200 when updated.",
				Body:        *new(In), Response: *new(Out),
				Handler: func(c *httpx.Ctx) (any, error) {
					var in In
					if err := c.Decode(&in); err != nil {
						return nil, err
					}
					ext := c.Param("externalId")
					reflect.ValueOf(&in).Elem().FieldByName("ExternalID").Set(reflect.ValueOf(&ext))
					var out Out
					created := false
					err := c.InTx(func(tx pgx.Tx) error {
						before, err := r.Get(c, tx, "external_id", ext, true)
						var p *httpx.Problem
						if errors.As(err, &p) && p.Code == "not_found" {
							created = true
							out, err = r.Insert(c, tx, in)
							return err
						}
						if err != nil {
							return err
						}
						out, err = r.Update(c, tx, before, in)
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
			})
	}
	if r.Archive {
		routes = append(routes, httpx.Route{
			Method: "DELETE", Path: r.Path + "/{id}", Tag: r.Tag, Feature: r.Feature, Scope: r.WriteScope,
			Summary: "Archive a " + name, Description: "Soft delete: history is kept.", Response: *new(Out),
			Handler: func(c *httpx.Ctx) (any, error) {
				var out Out
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := r.getScoped(c, tx, true)
					if err != nil {
						return err
					}
					sets := "archived_at = coalesce(archived_at, now())"
					if r.hasUpdatedAt {
						sets += ", updated_at = now()"
					}
					out, err = r.scan(tx.QueryRow(c, `UPDATE `+r.Table+` SET `+sets+` WHERE id = $1 RETURNING `+r.selectList(), idOf(before)))
					if err != nil {
						return err
					}
					return r.record(c, tx, "archive", r.ArchivedEvent, &before, &out)
				})
				return out, err
			},
		})
	}
	return routes
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }

// Now is overridable in tests.
var Now = time.Now
