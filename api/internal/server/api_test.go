package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/selectdev/purros/api/internal/testutil"
	"github.com/selectdev/purros/api/internal/webhooks"
)

var allScopes = []string{
	"organization:read", "people:read", "people:write", "time:read", "time:write",
	"sales:read", "sales:write", "inventory:read", "inventory:write",
}

func TestAuthentication(t *testing.T) {
	env := testutil.New(t, []string{"organization:read"}, nil)

	env.DoWithKey("", "GET", "/api/v1/me", nil).Expect(t, 401)
	env.DoWithKey("pk_live_nope", "GET", "/api/v1/me", nil).Expect(t, 401)
	r := env.Do("GET", "/api/v1/me", nil).Expect(t, 200)
	if r.Str("type") != "integration" || r.Str("integration.name") != "test-integration" {
		t.Fatalf("unexpected /me: %s", r.Raw)
	}
	// Missing scope.
	r = env.Do("GET", "/api/v1/employees", nil).Expect(t, 403)
	if r.Str("code") != "forbidden" {
		t.Fatalf("expected forbidden, got %s", r.Raw)
	}
	// Scoped read works.
	env.Do("GET", "/api/v1/locations", nil).Expect(t, 200)

	// Revoked keys stop working.
	if _, err := env.Pool.Exec(context.Background(), `UPDATE api_keys SET revoked_at = now()`); err != nil {
		t.Fatal(err)
	}
	env.Do("GET", "/api/v1/me", nil).Expect(t, 401)

	// Unknown routes and methods.
	env.DoWithKey("", "GET", "/api/v1/nope", nil).Expect(t, 404)
	env.DoWithKey("", "DELETE", "/api/v1/openapi.json", nil).Expect(t, 405)
}

func TestEmployeeLifecycle(t *testing.T) {
	env := testutil.New(t, allScopes, []string{"employee.created", "employee.updated", "employee.terminated"})

	// Validation lists every field problem by JSON path.
	r := env.Do("POST", "/api/v1/employees", map[string]any{"email": "not-an-email", "employmentType": "pirate"}).Expect(t, 422)
	paths := []string{}
	for _, e := range r.Get("errors").([]any) {
		paths = append(paths, e.(map[string]any)["path"].(string))
	}
	for _, want := range []string{"firstName", "lastName", "email", "employmentType"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("missing validation error for %s: %v", want, paths)
		}
	}

	// Unknown references become field errors.
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "A", "lastName": "B", "homeLocationId": "loc_nope"}).Expect(t, 422)

	// Create.
	r = env.Do("POST", "/api/v1/employees", map[string]any{
		"externalId": "hr-1", "firstName": "Dana", "lastName": "Reyes", "email": "dana@example.com",
		"homeLocationId": env.LocationID, "startDate": "2025-03-01",
		"integrationData": map[string]any{"test-integration": map[string]any{"badge": "0417"}},
	}).Expect(t, 201)
	id := r.Str("id")
	if !strings.HasPrefix(id, "emp_") || r.Str("startDate") != "2025-03-01" || r.Get("version").(float64) != 1 {
		t.Fatalf("unexpected employee: %s", r.Raw)
	}

	// Another integration's namespace can't be written.
	env.Do("PATCH", "/api/v1/employees/"+id, map[string]any{"integrationData": map[string]any{"other": 1}}).Expect(t, 422)

	// Duplicate external ID.
	env.Do("POST", "/api/v1/employees", map[string]any{"externalId": "hr-1", "firstName": "X", "lastName": "Y"}).Expect(t, 409)

	// Get by ID and external ID.
	env.Do("GET", "/api/v1/employees/"+id, nil).Expect(t, 200)
	if env.Do("GET", "/api/v1/employees/external/hr-1", nil).Expect(t, 200).Str("id") != id {
		t.Fatal("external lookup returned a different employee")
	}

	// Optimistic concurrency.
	env.Do("PATCH", "/api/v1/employees/"+id, map[string]any{"position": "Barista"}, "If-Match", "5").Expect(t, 409)
	r = env.Do("PATCH", "/api/v1/employees/"+id, map[string]any{"position": "Barista", "email": nil}, "If-Match", "1").Expect(t, 200)
	if r.Str("position") != "Barista" || r.Get("email") != nil || r.Str("lastName") != "Reyes" {
		t.Fatalf("patch should change only sent fields: %s", r.Raw)
	}

	// Upsert by external ID: 201 then 200.
	env.Do("PUT", "/api/v1/employees/external/hr-2", map[string]any{"firstName": "Sam", "lastName": "Lee"}).Expect(t, 201)
	r = env.Do("PUT", "/api/v1/employees/external/hr-2", map[string]any{"firstName": "Samuel", "lastName": "Lee"}).Expect(t, 200)
	if r.Str("firstName") != "Samuel" {
		t.Fatalf("upsert did not update: %s", r.Raw)
	}

	// Terminate.
	env.Do("POST", "/api/v1/employees/"+id+":terminate", map[string]any{"endDate": "2020-01-01"}).Expect(t, 422)
	r = env.Do("POST", "/api/v1/employees/"+id+":terminate", map[string]any{"endDate": "2026-09-30", "reason": "Moved away"}).Expect(t, 200)
	if r.Str("status") != "terminated" || r.Str("endDate") != "2026-09-30" {
		t.Fatalf("terminate: %s", r.Raw)
	}
	env.Do("POST", "/api/v1/employees/"+id+":terminate", map[string]any{"endDate": "2026-09-30"}).Expect(t, 409)

	// Filters and pagination.
	r = env.Do("GET", "/api/v1/employees?status=terminated", nil).Expect(t, 200)
	if len(r.Get("data").([]any)) != 1 {
		t.Fatalf("status filter: %s", r.Raw)
	}
	r = env.Do("GET", "/api/v1/employees?limit=1", nil).Expect(t, 200)
	if r.Get("hasMore") != true || r.Str("nextCursor") == "" {
		t.Fatalf("expected another page: %s", r.Raw)
	}
	r2 := env.Do("GET", "/api/v1/employees?limit=1&cursor="+r.Str("nextCursor"), nil).Expect(t, 200)
	if r2.Str("data.0.id") == r.Str("data.0.id") || r2.Get("hasMore") != false {
		t.Fatalf("second page wrong: %s", r2.Raw)
	}

	// Audit log and webhooks.
	var audits int
	_ = env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE entity_id = $1`, id).Scan(&audits)
	if audits != 3 { // create, update, terminate
		t.Fatalf("expected 3 audit entries, got %d", audits)
	}
	env.ProcessWebhooks()
	// Events about the same employee arrive in order.
	perRecord := map[string][]string{}
	for _, ev := range env.Hooks.Events {
		var d struct {
			Object struct{ ID string } `json:"object"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		perRecord[d.Object.ID] = append(perRecord[d.Object.ID], ev.Type)
	}
	if got := perRecord[id]; !slices.Equal(got, []string{"employee.created", "employee.updated", "employee.terminated"}) {
		t.Fatalf("webhooks for %s out of order: %v", id, got)
	}
	if len(env.Hooks.Events) != 5 {
		t.Fatalf("expected 5 webhooks, got %v", env.Hooks.Types())
	}
	if env.Hooks.BadSigs != 0 {
		t.Fatal("webhook signatures failed verification")
	}
	var ev webhooks.Envelope
	for _, e := range env.Hooks.Events {
		if e.Type == "employee.updated" {
			ev = e
			break
		}
	}
	if !strings.Contains(string(ev.Data), `"previous"`) || !strings.Contains(string(ev.Actor), "Test Integration") {
		t.Fatalf("update event missing previous values or actor: %s %s", ev.Data, ev.Actor)
	}
}

func TestPunchIngestion(t *testing.T) {
	env := testutil.New(t, allScopes, nil)
	emp := env.Do("POST", "/api/v1/employees", map[string]any{"externalId": "hr-1", "firstName": "A", "lastName": "B"}).Expect(t, 201).Str("id")

	batch := map[string]any{"source": "timeclock:lobby", "punches": []any{
		map[string]any{"employeeExternalId": "hr-1", "type": "in", "at": "2026-09-27T13:00:00Z", "deviceId": "d1", "locationExternalId": "101"},
		map[string]any{"employeeId": emp, "type": "out", "at": "2026-09-27T21:00:00Z", "deviceId": "d1"},
		map[string]any{"employeeExternalId": "ghost", "type": "in", "at": "2026-09-27T13:00:00Z"},
		map[string]any{"employeeId": emp, "type": "nap", "at": "2026-09-27T13:00:00Z"},
		map[string]any{"employeeId": emp, "type": "in", "at": "2026-09-27T13:00:00Z", "locationExternalId": "nope"},
	}}
	r := env.Do("POST", "/api/v1/time/punches:batch", batch).Expect(t, 202)
	statuses := []string{}
	for _, res := range r.Get("results").([]any) {
		statuses = append(statuses, res.(map[string]any)["status"].(string))
	}
	if !slices.Equal(statuses, []string{"created", "created", "rejected", "rejected", "rejected"}) {
		t.Fatalf("statuses: %v (%s)", statuses, r.Raw)
	}
	// Re-sending is safe.
	r = env.Do("POST", "/api/v1/time/punches:batch", batch).Expect(t, 202)
	if r.Get("created").(float64) != 0 || r.Str("results.0.status") != "duplicate" {
		t.Fatalf("resend should be duplicates: %s", r.Raw)
	}
	r = env.Do("GET", "/api/v1/time/punches?employeeId="+emp, nil).Expect(t, 200)
	if len(r.Get("data").([]any)) != 2 {
		t.Fatalf("expected 2 punches: %s", r.Raw)
	}
	// Empty and oversized batches.
	env.Do("POST", "/api/v1/time/punches:batch", map[string]any{"source": "x", "punches": []any{}}).Expect(t, 422)
	env.Do("POST", "/api/v1/time/punches:batch", map[string]any{"source": "has space", "punches": []any{map[string]any{}}}).Expect(t, 422)
}

func txn(ext, occurred, total string, lines []any, tenders []any) map[string]any {
	return map[string]any{"externalId": ext, "locationExternalId": "101", "occurredAt": occurred,
		"lines": lines, "tenders": tenders, "total": total}
}

func TestSalesIngestion(t *testing.T) {
	env := testutil.New(t, allScopes, []string{"sales.unmapped_item"})
	latte := env.Do("POST", "/api/v1/items", map[string]any{"sku": "LATTE", "name": "Latte"}).Expect(t, 201).Str("id")

	lines := []any{
		map[string]any{"itemSku": "LATTE", "quantity": "2", "unitPrice": "4.50"},
		map[string]any{"itemExternalId": "pos-775", "name": "Muffin", "quantity": "1", "unitPrice": "3.25"},
	}
	batch := map[string]any{"source": "pos:store-101", "transactions": []any{
		// 02:30 in Chicago on the 28th, before the 04:00 cut-off → business day 27th.
		txn("t1", "2026-09-28T07:30:00Z", "12.25", lines, []any{
			map[string]any{"type": "card", "amount": "9.00"},
			map[string]any{"type": "cash", "amount": "3.30", "change": "0.05"},
		}),
		txn("t2", "2026-09-28T15:00:00Z", "6.00", nil, []any{map[string]any{"type": "card", "amount": "5.00"}}),
		txn("t3", "2026-09-28T15:00:00Z", "1.00", nil, []any{map[string]any{"type": "bitcoin", "amount": "1.00"}}),
		map[string]any{"externalId": "t4", "locationExternalId": "999", "occurredAt": "2026-09-28T15:00:00Z", "total": "1"},
	}}
	r := env.Do("POST", "/api/v1/sales/transactions:batch", batch).Expect(t, 202)
	if r.Str("results.0.status") != "created" || r.Get("created").(float64) != 1 || r.Get("rejected").(float64) != 3 {
		t.Fatalf("batch results: %s", r.Raw)
	}
	if !strings.Contains(r.Str("results.1.errors.0.message"), "does not match") ||
		r.Str("results.2.errors.0.path") != "tenders[0].type" ||
		r.Str("results.3.errors.0.path") != "location" {
		t.Fatalf("rejection reasons: %s", r.Raw)
	}

	r = env.Do("GET", "/api/v1/sales/transactions?source=pos:store-101", nil).Expect(t, 200)
	if r.Str("data.0.businessDate") != "2026-09-27" {
		t.Fatalf("business date should respect the cut-off: %s", r.Raw)
	}
	if r.Str("data.0.lines.0.itemId") != latte || r.Get("data.0.lines.1.itemId") != nil {
		t.Fatalf("item matching: %s", r.Raw)
	}
	if r.Str("data.0.total") != "12.25" || r.Str("data.0.currency") != "USD" {
		t.Fatalf("money fields: %s", r.Raw)
	}

	// Unmapped items and mapping.
	r = env.Do("GET", "/api/v1/sales/unmapped-items", nil).Expect(t, 200)
	if r.Str("data.0.ref") != "externalId:pos-775" {
		t.Fatalf("unmapped: %s", r.Raw)
	}
	muffin := env.Do("POST", "/api/v1/items", map[string]any{"sku": "MUF", "name": "Muffin"}).Expect(t, 201).Str("id")
	r = env.Do("POST", "/api/v1/sales/unmapped-items:map", map[string]any{"source": "pos:store-101", "ref": "externalId:pos-775", "itemId": muffin}).Expect(t, 200)
	if r.Get("linesUpdated").(float64) != 1 {
		t.Fatalf("map: %s", r.Raw)
	}
	if n := len(env.Do("GET", "/api/v1/sales/unmapped-items", nil).Expect(t, 200).Get("data").([]any)); n != 0 {
		t.Fatalf("expected no unmapped items, got %d", n)
	}

	// Re-sending updates, and the saved mapping is used.
	r = env.Do("POST", "/api/v1/sales/transactions:batch", map[string]any{"source": "pos:store-101", "transactions": []any{
		txn("t1", "2026-09-28T07:30:00Z", "12.25", lines, nil),
	}}).Expect(t, 202)
	if r.Str("results.0.status") != "updated" {
		t.Fatalf("resend: %s", r.Raw)
	}
	r = env.Do("GET", "/api/v1/sales/transactions", nil).Expect(t, 200)
	if r.Str("data.0.lines.1.itemId") != muffin || r.Get("data.0.version").(float64) != 2 {
		t.Fatalf("resend should use mapping and bump version: %s", r.Raw)
	}

	// Summaries.
	r = env.Do("POST", "/api/v1/sales-summaries", map[string]any{"source": "pos:store-101", "summaries": []any{
		map[string]any{"externalId": "h12", "locationExternalId": "101", "periodStart": "2026-09-27T17:00:00Z",
			"periodEnd": "2026-09-27T18:00:00Z", "netSales": "1840.25", "transactions": 212},
		map[string]any{"externalId": "bad", "locationExternalId": "101", "periodStart": "2026-09-27T18:00:00Z",
			"periodEnd": "2026-09-27T17:00:00Z", "netSales": "1"},
	}}).Expect(t, 202)
	if r.Str("results.0.status") != "created" || r.Str("results.1.errors.0.path") != "periodEnd" {
		t.Fatalf("summaries: %s", r.Raw)
	}
	r = env.Do("GET", "/api/v1/sales-summaries?from=2026-09-27&to=2026-09-27", nil).Expect(t, 200)
	if r.Str("data.0.netSales") != "1840.25" {
		t.Fatalf("summary list: %s", r.Raw)
	}

	env.ProcessWebhooks()
	if !slices.Equal(env.Hooks.Types(), []string{"sales.unmapped_item"}) {
		t.Fatalf("expected one unmapped-item event, got %v", env.Hooks.Types())
	}
}

func TestIdempotency(t *testing.T) {
	env := testutil.New(t, allScopes, nil)
	body := map[string]any{"firstName": "A", "lastName": "B"}
	r1 := env.Do("POST", "/api/v1/employees", body, "Idempotency-Key", "k1").Expect(t, 201)
	r2 := env.Do("POST", "/api/v1/employees", body, "Idempotency-Key", "k1").Expect(t, 201)
	if r1.Str("id") != r2.Str("id") || r2.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("retry with the same key should replay the original response")
	}
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "C", "lastName": "D"}, "Idempotency-Key", "k1").Expect(t, 409)
	var n int
	_ = env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM employees`).Scan(&n)
	if n != 1 {
		t.Fatalf("expected 1 employee, got %d", n)
	}
}

func TestFeatureSwitches(t *testing.T) {
	env := testutil.New(t, allScopes, []string{"employee.created"})
	ctx := context.Background()

	changed, err := env.App.Features.Disable(ctx, "people", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range []string{"time", "scheduling", "employee_area"} {
		if !slices.Contains(changed, dep) {
			t.Fatalf("disabling people should also switch off %s: %v", dep, changed)
		}
	}
	r := env.Do("GET", "/api/v1/employees", nil).Expect(t, 404)
	if r.Str("code") != "feature_disabled" {
		t.Fatalf("expected feature_disabled: %s", r.Raw)
	}
	env.Do("POST", "/api/v1/time/punches:batch", map[string]any{"source": "x", "punches": []any{}}).Expect(t, 404)

	spec := env.DoWithKey("", "GET", "/api/v1/openapi.json", nil).Expect(t, 200)
	for p := range spec.Get("paths").(map[string]any) {
		if strings.Contains(p, "employees") || strings.Contains(p, "punches") {
			t.Fatalf("disabled feature still in OpenAPI: %s", p)
		}
	}
	perms := env.Do("GET", "/api/v1/permissions", nil).Expect(t, 200)
	for _, p := range perms.Get("data").([]any) {
		if f := p.(map[string]any)["feature"].(string); f == "people" || f == "time" {
			t.Fatalf("disabled feature's permission listed: %v", p)
		}
	}

	// Events of disabled features aren't delivered.
	if _, err := env.App.Features.Enable(ctx, "people", "test"); err != nil {
		t.Fatal(err)
	}
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "A", "lastName": "B"}).Expect(t, 201)
	if _, err := env.App.Features.Disable(ctx, "people", "test"); err != nil {
		t.Fatal(err)
	}
	env.ProcessWebhooks()
	if len(env.Hooks.Types()) != 0 {
		t.Fatalf("events of a disabled feature were delivered: %v", env.Hooks.Types())
	}

	// Re-enabling restores dependents that were on before.
	if _, err := env.App.Features.Enable(ctx, "people", "test"); err != nil {
		t.Fatal(err)
	}
	env.Do("GET", "/api/v1/time/punches", nil).Expect(t, 200)
}

func TestWebhookRetries(t *testing.T) {
	env := testutil.New(t, allScopes, []string{"employee.created"})
	env.Hooks.FailNext = 1
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "A", "lastName": "B"}).Expect(t, 201)
	env.ProcessWebhooks()

	var status string
	var attempts int
	_ = env.Pool.QueryRow(context.Background(), `SELECT status, attempts FROM webhook_deliveries`).Scan(&status, &attempts)
	if status != "pending" || attempts != 1 {
		t.Fatalf("after a failure: status=%s attempts=%d", status, attempts)
	}
	// Make it due now and retry.
	_, _ = env.Pool.Exec(context.Background(), `UPDATE webhook_deliveries SET next_attempt_at = now()`)
	env.ProcessWebhooks()
	_ = env.Pool.QueryRow(context.Background(), `SELECT status, attempts FROM webhook_deliveries`).Scan(&status, &attempts)
	if status != "succeeded" || attempts != 2 || len(env.Hooks.Types()) != 1 {
		t.Fatalf("after retry: status=%s attempts=%d events=%v", status, attempts, env.Hooks.Types())
	}
}

func TestOpenAPIAndHealth(t *testing.T) {
	env := testutil.New(t, allScopes, nil)
	env.DoWithKey("", "GET", "/api/health", nil).Expect(t, 200)
	if env.DoWithKey("", "GET", "/api/ready", nil).Expect(t, 200).Str("status") != "ready" {
		t.Fatal("not ready")
	}
	spec := env.DoWithKey("", "GET", "/api/v1/openapi.json", nil).Expect(t, 200)
	if spec.Str("openapi") != "3.1.0" {
		t.Fatalf("bad spec: %.200s", spec.Raw)
	}
	paths := spec.Get("paths").(map[string]any)
	for _, p := range []string{"/api/v1/employees", "/api/v1/employees/{id}:terminate", "/api/v1/sales/transactions:batch", "/api/v1/time/punches:batch"} {
		if _, ok := paths[p]; !ok {
			t.Fatalf("spec is missing %s", p)
		}
	}
	schemas := spec.Get("components.schemas").(map[string]any)
	emp, ok := schemas["EmployeeInput"].(map[string]any)
	if !ok {
		t.Fatal("EmployeeInput schema missing")
	}
	req := fmt.Sprint(emp["required"])
	if !strings.Contains(req, "firstName") || strings.Contains(req, "email") {
		t.Fatalf("EmployeeInput required fields wrong: %s", req)
	}
	// Same-named types from different packages must get their own schemas.
	bodyRef := func(path string) string {
		return spec.Str("paths." + path + ".post.requestBody.content.application/json.schema.$ref")
	}
	refs := map[string]bool{}
	for _, p := range []string{"/api/v1/inventory/adjustments", "/api/v1/time-off/adjustments", "/api/v1/forecasts/adjustments"} {
		refs[bodyRef(p)] = true
	}
	if len(refs) != 3 || refs[""] {
		t.Fatalf("adjustment endpoints share schemas: %v", refs)
	}
}

func TestIntegrationSelfService(t *testing.T) {
	env := testutil.New(t, []string{"organization:read"}, nil)
	r := env.Do("GET", "/api/v1/integrations/self/config", nil).Expect(t, 200)
	if r.Str("token") != "s3cret" {
		t.Fatalf("config: %s", r.Raw)
	}
	env.Do("POST", "/api/v1/integrations/self/health", map[string]any{"status": "fine"}).Expect(t, 422)
	env.Do("POST", "/api/v1/integrations/self/health", map[string]any{"status": "ok", "message": "synced"}).Expect(t, 200)
	env.Do("POST", "/api/v1/integrations/self/logs", map[string]any{"message": "Imported 42 punches"}).Expect(t, 201)
	r = env.Do("GET", "/api/v1/integrations/self", nil).Expect(t, 200)
	if r.Str("healthStatus") != "ok" || r.Str("healthMessage") != "synced" {
		t.Fatalf("health not stored: %s", r.Raw)
	}
}
