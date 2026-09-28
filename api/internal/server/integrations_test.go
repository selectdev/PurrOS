package server_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/selectdev/purros/api/internal/cli"
	"github.com/selectdev/purros/api/internal/testutil"
)

func TestOrganizationWrites(t *testing.T) {
	env := testutil.New(t, []string{"organization:read", "organization:write"}, []string{
		"location.created", "location.updated", "location.archived", "org_unit.created", "org_unit.updated", "department.created"})

	region := env.Do("POST", "/api/v1/org-units", map[string]any{"levelName": "Region", "name": "North"}).Expect(t, 201)
	district := env.Do("POST", "/api/v1/org-units", map[string]any{"levelName": "District", "name": "North-East",
		"parentId": region.Str("id")}).Expect(t, 201)
	env.Do("POST", "/api/v1/org-units", map[string]any{"levelName": "District", "name": "X", "parentId": "org_nope"}).Expect(t, 422)
	// A unit can't be moved under its own descendant, or itself.
	r := env.Do("PATCH", "/api/v1/org-units/"+region.Str("id"), map[string]any{"parentId": district.Str("id")}).Expect(t, 422)
	if r.Str("errors.0.path") != "parentId" {
		t.Fatalf("cycle: %s", r.Raw)
	}
	env.Do("PATCH", "/api/v1/org-units/"+region.Str("id"), map[string]any{"parentId": region.Str("id")}).Expect(t, 422)

	// Locations.
	r = env.Do("POST", "/api/v1/locations", map[string]any{"name": "Store 7", "timezone": "Mars/Base", "businessDayCutoff": "25:00"}).Expect(t, 422)
	if len(r.Get("errors").([]any)) != 2 {
		t.Fatalf("location validation: %s", r.Raw)
	}
	loc := env.Do("POST", "/api/v1/locations", map[string]any{"name": "Store 7", "code": "7", "orgUnitId": district.Str("id"),
		"timezone": "Europe/London", "currency": "GBP", "businessDayCutoff": "04:00", "address": map[string]any{"city": "York"}}).Expect(t, 201)
	if loc.Str("status") != "open" || loc.Str("businessDayCutoff") != "04:00" || loc.Str("address.city") != "York" || loc.Get("version") != 1.0 {
		t.Fatalf("location: %s", loc.Raw)
	}
	id := loc.Str("id")
	env.Do("PATCH", "/api/v1/locations/"+id, map[string]any{"name": "Store 7b"}, "If-Match", "9").Expect(t, 409)
	r = env.Do("PATCH", "/api/v1/locations/"+id, map[string]any{"name": "Store 7b", "status": "temporarily_closed"}, "If-Match", "1").Expect(t, 200)
	if r.Str("name") != "Store 7b" || r.Str("timezone") != "Europe/London" || r.Str("status") != "temporarily_closed" {
		t.Fatalf("patch kept the other fields: %s", r.Raw)
	}
	env.Do("PUT", "/api/v1/locations/external/777", map[string]any{"name": "Web warehouse"}).Expect(t, 201)
	r = env.Do("PUT", "/api/v1/locations/external/777", map[string]any{"name": "Web warehouse", "timezone": "America/Denver"}).Expect(t, 200)
	if r.Str("timezone") != "America/Denver" {
		t.Fatalf("upsert: %s", r.Raw)
	}
	env.Do("POST", "/api/v1/locations", map[string]any{"name": "Dup", "externalId": "777"}).Expect(t, 409)

	// An org unit that still contains a location can't be archived.
	env.Do("DELETE", "/api/v1/org-units/"+district.Str("id"), nil).Expect(t, 409)
	r = env.Do("DELETE", "/api/v1/locations/"+id, nil).Expect(t, 200)
	if r.Str("status") != "closed" || r.Get("archivedAt") == nil {
		t.Fatalf("archive: %s", r.Raw)
	}
	env.Do("DELETE", "/api/v1/org-units/"+district.Str("id"), nil).Expect(t, 200)
	list := env.Do("GET", "/api/v1/locations", nil).Expect(t, 200)
	all := env.Do("GET", "/api/v1/locations?includeArchived=true", nil).Expect(t, 200)
	if len(all.Get("data").([]any)) != len(list.Get("data").([]any))+1 {
		t.Fatalf("archived locations are hidden by default: %s / %s", list.Raw, all.Raw)
	}

	// Departments.
	env.Do("POST", "/api/v1/departments", map[string]any{"name": "Kitchen", "externalId": "kitchen"}).Expect(t, 201)
	env.Do("PUT", "/api/v1/departments/external/front", map[string]any{"name": "Front of house"}).Expect(t, 201)
	if n := len(env.Do("GET", "/api/v1/departments", nil).Expect(t, 200).Get("data").([]any)); n != 2 {
		t.Fatalf("departments: %d", n)
	}

	// The CLI emits location.created too.
	if _, err := cli.CreateLocation(context.Background(), env.Pool, cli.LocationInput{Name: "CLI store", Timezone: "UTC", Currency: "USD", Cutoff: "00:00"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CreateLocation(context.Background(), env.Pool, cli.LocationInput{Name: "Bad", Timezone: "Nope", Cutoff: "00:00"}); err == nil ||
		!strings.Contains(err.Error(), "timezone") {
		t.Fatalf("CLI validation: %v", err)
	}

	env.ProcessWebhooks()
	got := env.Hooks.Types()
	for _, want := range []string{"org_unit.created", "location.created", "location.updated", "location.archived", "department.created"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	created := 0
	for _, e := range got {
		if e == "location.created" {
			created++
		}
	}
	if created != 4 { // the harness's Store 101, Store 7, the upserted warehouse and the CLI store
		t.Errorf("location.created %d times: %v", created, got)
	}

	// Integration keys without organization:write can't change the structure.
	ro := testutil.New(t, []string{"organization:read"}, nil)
	ro.Do("POST", "/api/v1/departments", map[string]any{"name": "Nope"}).Expect(t, 403)
}

func TestOrganizationReach(t *testing.T) {
	env := testutil.New(t, []string{"organization:read"}, nil)
	other, err := cli.CreateLocation(context.Background(), env.Pool, cli.LocationInput{Name: "Store 102", Timezone: "UTC", Currency: "USD", Cutoff: "00:00"})
	if err != nil {
		t.Fatal(err)
	}
	env.CreateUser(testutil.UserSpec{Email: "gm@test", Perms: map[string]string{"organization.manage": "assigned_locations"},
		LocationIDs: []string{env.LocationID}})
	s := env.SignIn("gm@test", "")
	s.Do("PATCH", "/api/v1/locations/"+env.LocationID, map[string]any{"name": "Store 101 (renamed)"}).Expect(t, 200)
	s.Do("PATCH", "/api/v1/locations/"+other, map[string]any{"name": "Not mine"}).Expect(t, 403)
	s.Do("POST", "/api/v1/org-units", map[string]any{"levelName": "Region", "name": "Mine"}).Expect(t, 403)
	s.Do("POST", "/api/v1/locations", map[string]any{"name": "New"}).Expect(t, 403)
}

func manifest(name, hookURL string, events ...string) map[string]any {
	m := map[string]any{
		"name": name, "displayName": "POS bridge", "version": "1.0.0",
		"scopes": []string{"sales:write", "people:read", "organization:read"},
		"config": []any{
			map[string]any{"key": "token", "type": "secret", "required": true},
			map[string]any{"key": "interval", "type": "number"},
			map[string]any{"key": "store", "type": "location"},
			map[string]any{"key": "mode", "type": "select", "options": []string{"live", "test"}},
		},
	}
	if hookURL != "" {
		m["webhooks"] = map[string]any{"url": hookURL, "events": events}
	}
	return m
}

func TestIntegrationAdminAPI(t *testing.T) {
	env := testutil.New(t, allScopes, nil)
	env.OwnerID()
	owner := env.SignIn("owner@test", "")
	rv, hookURL := testutil.NewReceiver(t)

	// People only, with integrations.manage.
	env.Do("GET", "/api/v1/integrations", nil).Expect(t, 403)
	env.CreateUser(testutil.UserSpec{Email: "it@test", Perms: map[string]string{"integrations.manage": "everyone"}})
	it := env.SignIn("it@test", "")
	env.CreateUser(testutil.UserSpec{Email: "nobody@test"})
	env.SignIn("nobody@test", "").Do("GET", "/api/v1/integrations", nil).Expect(t, 403)

	// Validation.
	bad := manifest("pos-bridge", hookURL, "cash.deposit_mismatch")
	r := owner.Do("POST", "/api/v1/integrations", map[string]any{"manifest": bad, "config": map[string]any{"token": "x"}}).Expect(t, 422)
	if !strings.Contains(string(r.Raw), "needs a cash") {
		t.Fatalf("events must be covered by scopes: %s", r.Raw)
	}
	r = owner.Do("POST", "/api/v1/integrations", map[string]any{"manifest": manifest("pos-bridge", "")}).Expect(t, 422)
	if r.Str("errors.0.path") != "config" {
		t.Fatalf("required config: %s", r.Raw)
	}
	owner.Do("POST", "/api/v1/integrations", map[string]any{"manifest": manifest("pos-bridge", ""),
		"config": map[string]any{"token": "x", "store": "loc_nope", "mode": "chaos"}}).Expect(t, 422)
	sens := manifest("hr-sync", "")
	sens["scopes"] = []string{"people:read", "people:sensitive"}
	it.Do("POST", "/api/v1/integrations", map[string]any{"manifest": sens, "config": map[string]any{"token": "x"},
		"approveSensitive": true}).Expect(t, 403)

	// Register.
	reg := it.Do("POST", "/api/v1/integrations", map[string]any{"manifest": manifest("pos-bridge", hookURL, "employee.created"),
		"config": map[string]any{"token": "abc", "interval": 5, "store": env.LocationID, "mode": "live"}}).Expect(t, 201)
	key, id := reg.Str("apiKey"), reg.Str("integration.id")
	if !strings.HasPrefix(key, "pk_live_") || reg.Str("webhookSecret") == "" || reg.Str("integration.status") != "active" {
		t.Fatalf("register: %s", reg.Raw)
	}
	rv.SetSecret(reg.Str("webhookSecret"))
	it.Do("POST", "/api/v1/integrations", map[string]any{"manifest": manifest("pos-bridge", ""), "config": map[string]any{"token": "x"}}).Expect(t, 409)
	env.DoWithKey(key, "GET", "/api/v1/integrations/self", nil).Expect(t, 200)
	if cfg := env.DoWithKey(key, "GET", "/api/v1/integrations/self/config", nil).Expect(t, 200); cfg.Str("token") != "abc" || cfg.Get("interval") != 5.0 {
		t.Fatalf("self config: %s", cfg.Raw)
	}
	d := it.Do("GET", "/api/v1/integrations/"+id, nil).Expect(t, 200)
	if d.Str("config.token") != "********" || d.Str("manifest.name") != "pos-bridge" ||
		len(d.Get("apiKeys").([]any)) != 1 || len(d.Get("webhookEndpoints").([]any)) != 1 {
		t.Fatalf("detail: %s", d.Raw)
	}
	if n := len(it.Do("GET", "/api/v1/integrations", nil).Expect(t, 200).Get("data").([]any)); n != 2 {
		t.Fatalf("list: %d", n) // the test harness's own integration and this one
	}

	// Its webhooks arrive, signed with its secret.
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Ann", "lastName": "Lee"}).Expect(t, 201)
	env.ProcessWebhooks()
	if got := rv.Types(); !slices.Equal(got, []string{"employee.created"}) || rv.BadSigs != 0 {
		t.Fatalf("integration webhooks: %v (bad signatures %d)", got, rv.BadSigs)
	}

	// Pausing refuses its key and holds its webhooks.
	it.Do("POST", "/api/v1/integrations/"+id+":pause", nil).Expect(t, 200)
	env.DoWithKey(key, "GET", "/api/v1/integrations/self", nil).Expect(t, 401)
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Bo", "lastName": "Held"}).Expect(t, 201)
	env.ProcessWebhooks()
	if len(rv.Types()) != 1 {
		t.Fatalf("paused integration received %v", rv.Types())
	}
	it.Do("POST", "/api/v1/integrations/"+id+":resume", nil).Expect(t, 200)
	env.ProcessWebhooks()
	if len(rv.Types()) != 2 {
		t.Fatalf("held delivery wasn't sent after resuming: %v", rv.Types())
	}

	// Key rotation: with a grace period the old key keeps working; without, it stops.
	rot := it.Do("POST", "/api/v1/integrations/"+id+":rotate-key", map[string]any{"graceMinutes": 30}).Expect(t, 200)
	env.DoWithKey(key, "GET", "/api/v1/integrations/self", nil).Expect(t, 200)
	env.DoWithKey(rot.Str("apiKey"), "GET", "/api/v1/integrations/self", nil).Expect(t, 200)
	rot2 := it.Do("POST", "/api/v1/integrations/"+id+":rotate-key", map[string]any{"graceMinutes": 0}).Expect(t, 200)
	env.DoWithKey(key, "GET", "/api/v1/integrations/self", nil).Expect(t, 401)
	env.DoWithKey(rot.Str("apiKey"), "GET", "/api/v1/integrations/self", nil).Expect(t, 401)
	key = rot2.Str("apiKey")
	env.DoWithKey(key, "GET", "/api/v1/integrations/self", nil).Expect(t, 200)
	it.Do("POST", "/api/v1/integrations/"+id+":rotate-key", map[string]any{"graceMinutes": 99999}).Expect(t, 422)

	// Config changes; null removes a value, and required values must stay.
	r = it.Do("PATCH", "/api/v1/integrations/"+id+"/config", map[string]any{"values": map[string]any{"token": "new", "interval": nil}}).Expect(t, 200)
	if r.Str("config.token") != "********" || r.Get("config.interval") != nil {
		t.Fatalf("config: %s", r.Raw)
	}
	if env.DoWithKey(key, "GET", "/api/v1/integrations/self/config", nil).Expect(t, 200).Str("token") != "new" {
		t.Fatal("config not changed")
	}
	it.Do("PATCH", "/api/v1/integrations/"+id+"/config", map[string]any{"values": map[string]any{"token": nil}}).Expect(t, 422)

	// Activity: logs and batches.
	env.DoWithKey(key, "POST", "/api/v1/integrations/self/logs", map[string]any{"message": "Imported 2 summaries", "level": "info"}).Expect(t, 201)
	env.DoWithKey(key, "POST", "/api/v1/sales-summaries", map[string]any{"source": "pos:store-101", "summaries": []any{
		map[string]any{"externalId": "h12", "locationExternalId": "101", "periodStart": "2026-09-27T17:00:00Z",
			"periodEnd": "2026-09-27T18:00:00Z", "netSales": "10"},
		map[string]any{"externalId": "bad", "locationExternalId": "101", "periodStart": "2026-09-27T18:00:00Z",
			"periodEnd": "2026-09-27T17:00:00Z", "netSales": "1"},
	}}).Expect(t, 202)
	if logs := it.Do("GET", "/api/v1/integrations/"+id+"/logs?level=info", nil).Expect(t, 200); logs.Str("data.0.message") != "Imported 2 summaries" {
		t.Fatalf("logs: %s", logs.Raw)
	}
	b := it.Do("GET", "/api/v1/integrations/"+id+"/batches?rejectedOnly=true", nil).Expect(t, 200)
	if b.Get("data.0.received") != 2.0 || b.Get("data.0.rejected") != 1.0 {
		t.Fatalf("batches: %s", b.Raw)
	}

	// Manifest updates: the name is fixed, and webhooks can be removed and added.
	m2 := manifest("pos-bridge-2", "")
	it.Do("PUT", "/api/v1/integrations/"+id+"/manifest", map[string]any{"manifest": m2}).Expect(t, 422)
	m2 = manifest("pos-bridge", "")
	m2["version"] = "2.0.0"
	u := it.Do("PUT", "/api/v1/integrations/"+id+"/manifest", map[string]any{"manifest": m2}).Expect(t, 200)
	if u.Str("integration.version") != "2.0.0" || u.Str("webhookSecret") != "" {
		t.Fatalf("update: %s", u.Raw)
	}
	if len(it.Do("GET", "/api/v1/integrations/"+id, nil).Expect(t, 200).Get("webhookEndpoints").([]any)) != 0 {
		t.Fatal("removing webhooks from the manifest should remove the endpoint")
	}
	u = it.Do("PUT", "/api/v1/integrations/"+id+"/manifest", map[string]any{"manifest": manifest("pos-bridge", hookURL, "employee.updated")}).Expect(t, 200)
	if u.Str("webhookSecret") == "" || u.Str("webhookEndpointId") == "" {
		t.Fatalf("re-adding webhooks returns a new secret: %s", u.Raw)
	}

	// Remove.
	it.Do("DELETE", "/api/v1/integrations/"+id, nil).Expect(t, 200)
	env.DoWithKey(key, "GET", "/api/v1/integrations/self", nil).Expect(t, 401)
	it.Do("GET", "/api/v1/integrations/"+id, nil).Expect(t, 404)
}

func TestWebhookEndpointsAPI(t *testing.T) {
	env := testutil.New(t, allScopes, []string{"employee.created"})
	env.OwnerID()
	owner := env.SignIn("owner@test", "")
	rv, url := testutil.NewReceiver(t)

	env.Do("GET", "/api/v1/webhook-endpoints", nil).Expect(t, 403)
	owner.Do("POST", "/api/v1/webhook-endpoints", map[string]any{"url": url, "events": []string{"nope.never"}}).Expect(t, 422)
	owner.Do("POST", "/api/v1/webhook-endpoints", map[string]any{"url": "ftp://x", "events": []string{"employee.created"}}).Expect(t, 422)
	created := owner.Do("POST", "/api/v1/webhook-endpoints", map[string]any{"url": url, "events": []string{"employee.created"},
		"description": "Data warehouse"}).Expect(t, 201)
	id := created.Str("endpoint.id")
	rv.SetSecret(created.Str("secret"))
	if created.Str("endpoint.status") != "active" || created.Get("endpoint.integrationId") != nil {
		t.Fatalf("create: %s", created.Raw)
	}

	// Ping.
	ping := owner.Do("POST", "/api/v1/webhook-endpoints/"+id+":ping", nil).Expect(t, 202)
	env.ProcessWebhooks()
	if got := rv.Types(); !slices.Equal(got, []string{"webhook.ping"}) {
		t.Fatalf("ping: %v", got)
	}
	if d := owner.Do("GET", "/api/v1/webhook-deliveries/"+ping.Str("id"), nil).Expect(t, 200); d.Str("status") != "succeeded" ||
		d.Str("event.type") != "webhook.ping" || d.Str("event.data.object.endpointId") != id {
		t.Fatalf("delivery: %s", d.Raw)
	}

	// A failed delivery is retried on request.
	rv.FailNext = 1
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Ann", "lastName": "Lee"}).Expect(t, 201)
	env.ProcessWebhooks()
	pending := owner.Do("GET", "/api/v1/webhook-endpoints/"+id+"/deliveries?status=pending", nil).Expect(t, 200)
	if pending.Get("data.0.lastStatusCode") != 500.0 || pending.Get("data.0.attempts") != 1.0 {
		t.Fatalf("pending: %s", pending.Raw)
	}
	owner.Do("POST", "/api/v1/webhook-deliveries/"+pending.Str("data.0.id")+":retry", nil).Expect(t, 200)
	env.ProcessWebhooks()
	if got := rv.Types(); len(got) != 2 || got[1] != "employee.created" {
		t.Fatalf("after retry: %v", got)
	}

	// A disabled endpoint holds its deliveries until it's enabled again.
	owner.Do("PATCH", "/api/v1/webhook-endpoints/"+id, map[string]any{"status": "disabled"}).Expect(t, 200)
	env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Bo", "lastName": "Held"}).Expect(t, 201)
	env.ProcessWebhooks()
	if len(rv.Types()) != 2 {
		t.Fatalf("disabled endpoint received %v", rv.Types())
	}
	r := owner.Do("PATCH", "/api/v1/webhook-endpoints/"+id, map[string]any{"status": "active"}).Expect(t, 200)
	if r.Get("disabledAt") != nil || r.Get("consecutiveFailures") != 0.0 {
		t.Fatalf("enable: %s", r.Raw)
	}
	env.ProcessWebhooks()
	if len(rv.Types()) != 3 {
		t.Fatalf("held delivery not sent: %v", rv.Types())
	}

	// Failed deliveries (after the retry schedule ran out) can be queued in bulk.
	if _, err := env.Pool.Exec(context.Background(), `UPDATE webhook_deliveries SET status = 'failed' WHERE endpoint_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if q := owner.Do("POST", "/api/v1/webhook-endpoints/"+id+":retry-failed", nil).Expect(t, 200); q.Get("queued") != 3.0 {
		t.Fatalf("retry-failed: %s", q.Raw)
	}
	env.ProcessWebhooks()
	if len(rv.Types()) != 6 {
		t.Fatalf("replayed: %v", rv.Types())
	}

	// A rotated secret signs the next delivery.
	sec := owner.Do("POST", "/api/v1/webhook-endpoints/"+id+":rotate-secret", nil).Expect(t, 200)
	rv.SetSecret(sec.Str("secret"))
	owner.Do("POST", "/api/v1/webhook-endpoints/"+id+":ping", nil).Expect(t, 202)
	env.ProcessWebhooks()
	if rv.BadSigs != 0 || len(rv.Types()) != 7 {
		t.Fatalf("after rotation: %v, bad signatures %d", rv.Types(), rv.BadSigs)
	}

	// Integration endpoints are managed by their manifest.
	all := owner.Do("GET", "/api/v1/webhook-endpoints", nil).Expect(t, 200)
	standalone := owner.Do("GET", "/api/v1/webhook-endpoints?integrationId=none", nil).Expect(t, 200)
	if len(all.Get("data").([]any)) != 2 || len(standalone.Get("data").([]any)) != 1 {
		t.Fatalf("lists: %s / %s", all.Raw, standalone.Raw)
	}
	var integ string
	for _, e := range all.Get("data").([]any) {
		if m := e.(map[string]any); m["integrationId"] != nil {
			integ = m["id"].(string)
		}
	}
	owner.Do("PATCH", "/api/v1/webhook-endpoints/"+integ, map[string]any{"url": "https://elsewhere.example"}).Expect(t, 409)
	owner.Do("DELETE", "/api/v1/webhook-endpoints/"+integ, nil).Expect(t, 409)
	owner.Do("PATCH", "/api/v1/webhook-endpoints/"+integ, map[string]any{"description": "Test integration"}).Expect(t, 200)

	owner.Do("DELETE", "/api/v1/webhook-endpoints/"+id, nil).Expect(t, 200)
	owner.Do("GET", "/api/v1/webhook-endpoints/"+id, nil).Expect(t, 404)
}
