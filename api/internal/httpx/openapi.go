package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// OpenAPI builds the OpenAPI 3.1 document for the routes of enabled features.
// Request and response schemas are derived from the Go types declared on each
// route, so the spec can't drift from the code.
func (a *App) OpenAPI(ctx context.Context, version string) (map[string]any, error) {
	g := &schemaGen{components: map[string]any{}}
	paths := map[string]map[string]any{}

	for _, r := range a.Router.Routes() {
		if r.Feature != "" {
			ok, err := a.Features.IsEnabled(ctx, r.Feature)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		op := map[string]any{
			"summary":     r.Summary,
			"operationId": operationID(r),
			"tags":        []string{r.Tag},
		}
		if r.Description != "" {
			op["description"] = r.Description
		}
		var desc []string
		if r.Scope != "" {
			desc = append(desc, "Scope: `"+r.Scope+"`.")
		}
		if r.Feature != "" && r.Feature != "core" {
			desc = append(desc, "Feature: `"+r.Feature+"`.")
		}
		if len(desc) > 0 {
			op["description"] = strings.TrimSpace(r.Description + "\n\n" + strings.Join(desc, " "))
		}
		if r.Auth == AuthNone {
			op["security"] = []any{}
		}

		var params []any
		for _, s := range r.segs {
			if s.param != "" {
				params = append(params, map[string]any{
					"name": s.param, "in": "path", "required": true, "schema": map[string]any{"type": "string"},
				})
			}
		}
		if r.Query != nil {
			params = append(params, g.queryParams(reflect.TypeOf(r.Query))...)
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if r.Body != nil {
			op["requestBody"] = map[string]any{
				"required": true,
				"content":  map[string]any{"application/json": map[string]any{"schema": g.schema(reflect.TypeOf(r.Body))}},
			}
		}
		resp := map[string]any{"description": http.StatusText(r.Status)}
		if r.Response != nil {
			resp["content"] = map[string]any{"application/json": map[string]any{"schema": g.schema(reflect.TypeOf(r.Response))}}
		}
		op["responses"] = map[string]any{
			itoa(r.Status): resp,
			"default": map[string]any{
				"description": "Error",
				"content": map[string]any{"application/problem+json": map[string]any{
					"schema": g.schema(reflect.TypeOf(Problem{})),
				}},
			},
		}

		p := APIPrefix + openAPIPath(r.Path)
		if paths[p] == nil {
			paths[p] = map[string]any{}
		}
		paths[p][strings.ToLower(r.Method)] = op
	}

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "PurrOS API",
			"version": version,
			"description": "REST API for PurrOS. Only endpoints of enabled features are listed. " +
				"See https://github.com/selectdev/PurrOS/tree/main/docs/api for guides.",
			"license": map[string]any{"name": "AGPL-3.0-only"},
		},
		"servers":  []any{map[string]any{"url": a.Config.URL}},
		"security": []any{map[string]any{"apiKey": []string{}}},
		"paths":    paths,
		"components": map[string]any{
			"schemas": g.components,
			"securitySchemes": map[string]any{
				"apiKey": map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "pk_live_…"},
			},
		},
	}, nil
}

// openAPIPath keeps the path as written; OpenAPI allows "{id}:terminate".
func openAPIPath(p string) string { return p }

func operationID(r *Route) string {
	s := strings.NewReplacer("/", "_", "{", "", "}", "", ":", "_", "-", "_").Replace(strings.Trim(r.Path, "/"))
	return strings.ToLower(r.Method) + "_" + s
}

func itoa(n int) string { return strconv.Itoa(n) }

type schemaGen struct {
	components map[string]any
}

var (
	timeType      = reflect.TypeOf(time.Time{})
	dateType      = reflect.TypeOf(Date{})
	timeOfDayType = reflect.TypeOf(TimeOfDay{})
	decimalType   = reflect.TypeOf(decimal.Decimal{})
	rawType       = reflect.TypeOf(json.RawMessage{})
)

func (g *schemaGen) schema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case dateType:
		return map[string]any{"type": "string", "format": "date", "example": "2026-09-27"}
	case timeOfDayType:
		return map[string]any{"type": "string", "pattern": `^\d{2}:\d{2}$`, "example": "09:00"}
	case decimalType:
		return map[string]any{"type": "string", "pattern": `^-?\d+(\.\d+)?$`, "example": "12.50"}
	case rawType:
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "format": "byte"}
		}
		return map[string]any{"type": "array", "items": g.schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.schema(t.Elem())}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Struct:
		name := schemaName(t)
		if name == "" {
			return g.structSchema(t)
		}
		if _, ok := g.components[name]; !ok {
			g.components[name] = map[string]any{} // placeholder for recursion
			g.components[name] = g.structSchema(t)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return map[string]any{}
}

func schemaName(t reflect.Type) string {
	name := t.Name()
	if name == "" {
		return ""
	}
	// Generic types like Page[Employee] → PageEmployee.
	if i := strings.IndexByte(name, '['); i >= 0 {
		inner := name[i+1 : len(name)-1]
		if j := strings.LastIndexByte(inner, '.'); j >= 0 {
			inner = inner[j+1:]
		}
		name = name[:i] + inner
	}
	return name
}

func (g *schemaGen) structSchema(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []string
	g.fields(t, props, &required)
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		sort.Strings(required)
		s["required"] = required
	}
	return s
}

func (g *schemaGen) fields(t reflect.Type, props map[string]any, required *[]string) {
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				g.fields(ft, props, required)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		s := g.schema(f.Type)
		if d := f.Tag.Get("doc"); d != "" {
			s = withExtra(s, "description", d)
		}
		validateTag := f.Tag.Get("validate")
		for _, rule := range strings.Split(validateTag, ",") {
			if enum, ok := strings.CutPrefix(rule, "oneof="); ok {
				s = withExtra(s, "enum", strings.Fields(enum))
			}
		}
		props[name] = s
		if isRequired(validateTag, opts, f.Type) {
			*required = append(*required, name)
		}
	}
}

// isRequired: a field is required if it has a "required" validation rule, or
// if it has no validation rules, isn't omitempty and isn't a pointer (always
// present in responses).
func isRequired(validateTag, jsonOpts string, t reflect.Type) bool {
	for _, rule := range strings.Split(validateTag, ",") {
		if rule == "required" {
			return true
		}
	}
	return validateTag == "" && !strings.Contains(jsonOpts, "omitempty") && t.Kind() != reflect.Pointer
}

func withExtra(s map[string]any, k string, v any) map[string]any {
	if _, isRef := s["$ref"]; isRef {
		return map[string]any{"allOf": []any{s}, k: v}
	}
	out := make(map[string]any, len(s)+1)
	for kk, vv := range s {
		out[kk] = vv
	}
	out[k] = v
	return out
}

func (g *schemaGen) queryParams(t reflect.Type) []any {
	var out []any
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous {
			out = append(out, g.queryParams(f.Type)...)
			continue
		}
		if name == "" || name == "-" {
			continue
		}
		p := map[string]any{"name": name, "in": "query", "schema": g.schema(f.Type)}
		if d := f.Tag.Get("doc"); d != "" {
			p["description"] = d
		}
		out = append(out, p)
	}
	return out
}
