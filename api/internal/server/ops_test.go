package server_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/selectdev/purros/api/internal/modules/insights"
	"github.com/selectdev/purros/api/internal/testutil"
)

func TestOperations(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"form.submitted", "form.answer_failed", "corrective_action.created",
		"corrective_action.closed", "audit.completed", "sensor.out_of_range"})
	form := env.Do("POST", "/api/v1/forms", map[string]any{"name": "Opening checklist", "questions": []any{
		map[string]any{"id": "fridge", "text": "Walk-in temperature", "type": "temperature", "max": "5", "required": true, "createAction": true},
		map[string]any{"id": "clean", "text": "Floors clean?", "type": "yes_no"},
		map[string]any{"id": "mood", "text": "Team mood", "type": "choice", "options": []string{"good", "bad"}},
		map[string]any{"id": "notes", "text": "Notes", "type": "text"},
	}}).Expect(t, 201)
	if form.Str("kind") != "checklist" {
		t.Fatalf("form: %s", form.Raw)
	}
	fid := form.Str("id")
	env.Do("POST", "/api/v1/forms", map[string]any{"name": "Bad", "questions": []any{
		map[string]any{"id": "a", "text": "A", "type": "yes_no"}, map[string]any{"id": "a", "text": "B", "type": "yes_no"}}}).Expect(t, 422)
	env.Do("POST", "/api/v1/forms", map[string]any{"name": "Bad", "questions": []any{
		map[string]any{"id": "a", "text": "A", "type": "choice"}}}).Expect(t, 422)

	// Missing required answer and a wrong type.
	env.Do("POST", "/api/v1/form-submissions", map[string]any{"formId": fid, "answers": map[string]any{"clean": true}}).Expect(t, 422)
	env.Do("POST", "/api/v1/form-submissions", map[string]any{"formId": fid, "answers": map[string]any{"fridge": "cold"}}).Expect(t, 422)

	sub := map[string]any{"formId": fid, "locationExternalId": "101", "source": "tablet", "externalId": "s-1",
		"answers": map[string]any{"fridge": 7.5, "clean": true, "mood": "good", "notes": "ok"}}
	s := env.Do("POST", "/api/v1/form-submissions", sub).Expect(t, 201)
	if s.Get("failedCount").(float64) != 1 || s.Get("passed") != true || s.Str("results.0.message") != "Above the maximum of 5" {
		t.Fatalf("submission: %s", s.Raw)
	}
	again := env.Do("POST", "/api/v1/form-submissions", sub).Expect(t, 200)
	if again.Str("id") != s.Str("id") {
		t.Fatalf("resend should return the existing submission: %s", again.Raw)
	}
	failed := env.Do("GET", "/api/v1/form-submissions?failed=true", nil).Expect(t, 200)
	if len(failed.Get("data").([]any)) != 1 {
		t.Fatalf("failed filter: %s", failed.Raw)
	}

	// The failed temperature opened a corrective action.
	acts := env.Do("GET", "/api/v1/corrective-actions?status=open", nil).Expect(t, 200)
	if acts.Str("data.0.title") != "Opening checklist: Walk-in temperature" || acts.Str("data.0.locationId") != env.LocationID {
		t.Fatalf("corrective action: %s", acts.Raw)
	}
	aid := acts.Str("data.0.id")
	closed := env.Do("POST", "/api/v1/corrective-actions/"+aid+":close", map[string]any{"resolution": "Door seal replaced"}).Expect(t, 200)
	if closed.Str("status") != "closed" || closed.Get("closedAt") == nil {
		t.Fatalf("close: %s", closed.Raw)
	}
	env.Do("POST", "/api/v1/corrective-actions/"+aid+":close", map[string]any{"resolution": "again"}).Expect(t, 409)

	// Audits are scored; a critical failure fails the audit.
	audit := env.Do("POST", "/api/v1/forms", map[string]any{"name": "Food safety audit", "kind": "audit", "questions": []any{
		map[string]any{"id": "q1", "text": "Hand sink stocked", "type": "yes_no", "weight": "3"},
		map[string]any{"id": "q2", "text": "Labels dated", "type": "yes_no", "weight": "1"},
		map[string]any{"id": "q3", "text": "No pests", "type": "yes_no", "critical": true},
	}}).Expect(t, 201).Str("id")
	a := env.Do("POST", "/api/v1/form-submissions", map[string]any{"formId": audit, "answers": map[string]any{"q1": true, "q2": false, "q3": true}}).Expect(t, 201)
	if a.Str("score") != "80" || a.Get("passed") != true {
		t.Fatalf("audit score: %s", a.Raw)
	}
	a = env.Do("POST", "/api/v1/form-submissions", map[string]any{"formId": audit, "answers": map[string]any{"q1": true, "q2": true, "q3": false}}).Expect(t, 201)
	if a.Str("score") != "80" || a.Get("passed") != false {
		t.Fatalf("critical failure: %s", a.Raw)
	}
	if n := len(env.Do("GET", "/api/v1/audits", nil).Expect(t, 200).Get("data").([]any)); n != 2 {
		t.Fatalf("audits: %d", n)
	}

	// Sensors raise out-of-range once until back in range.
	sensor := env.Do("POST", "/api/v1/sensors", map[string]any{"locationId": env.LocationID, "name": "Walk-in", "externalId": "probe-1", "maxValue": "5"}).Expect(t, 201)
	if sensor.Str("unit") != "°C" {
		t.Fatalf("sensor: %s", sensor.Raw)
	}
	r := env.Do("POST", "/api/v1/sensor-readings", map[string]any{"source": "iot", "readings": []any{
		map[string]any{"sensorExternalId": "probe-1", "at": "2026-09-27T10:00:00Z", "value": "3.1"},
		map[string]any{"sensorExternalId": "probe-1", "at": "2026-09-27T10:05:00Z", "value": "6.2"},
		map[string]any{"sensorExternalId": "probe-1", "at": "2026-09-27T10:10:00Z", "value": "6.8"},
		map[string]any{"sensorExternalId": "nope", "at": "2026-09-27T10:10:00Z", "value": "1"},
	}}).Expect(t, 202)
	if r.Str("results.3.status") != "rejected" {
		t.Fatalf("readings: %s", r.Raw)
	}
	readings := env.Do("GET", "/api/v1/sensors/"+sensor.Str("id")+"/readings", nil).Expect(t, 200)
	if len(readings.Get("data").([]any)) != 3 || readings.Get("data.0.inRange") != false {
		t.Fatalf("sensor readings: %s", readings.Raw)
	}

	env.ProcessWebhooks()
	got := env.Hooks.Types()
	if n := count(got, "sensor.out_of_range"); n != 1 {
		t.Fatalf("expected one out-of-range event, got %d: %v", n, got)
	}
	for _, want := range []string{"form.submitted", "form.answer_failed", "corrective_action.created", "corrective_action.closed", "audit.completed"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
}

func count(list []string, v string) int {
	n := 0
	for _, x := range list {
		if x == v {
			n++
		}
	}
	return n
}

func TestEquipment(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"maintenance.due", "work_order.created", "work_order.closed"})
	asset := env.Do("POST", "/api/v1/assets", map[string]any{"locationId": env.LocationID, "name": "Forklift", "meterUnit": "hours",
		"maintenanceEveryMeter": "250"}).Expect(t, 201).Str("id")

	env.Do("POST", "/api/v1/assets/"+asset+"/meter-readings", map[string]any{"value": "100"}).Expect(t, 201)
	if n := len(env.Do("GET", "/api/v1/work-orders", nil).Expect(t, 200).Get("data").([]any)); n != 0 {
		t.Fatalf("no work order expected yet")
	}
	a := env.Do("POST", "/api/v1/assets/"+asset+"/meter-readings", map[string]any{"value": "260"}).Expect(t, 201)
	if a.Str("meterValue") != "260" {
		t.Fatalf("meter: %s", a.Raw)
	}
	env.Do("POST", "/api/v1/assets/"+asset+"/meter-readings", map[string]any{"value": "270"}).Expect(t, 201)
	wos := env.Do("GET", "/api/v1/work-orders?kind=maintenance", nil).Expect(t, 200)
	if len(wos.Get("data").([]any)) != 1 || wos.Str("data.0.assetId") != asset || wos.Str("data.0.status") != "open" {
		t.Fatalf("maintenance work order: %s", wos.Raw)
	}
	due := env.Do("GET", "/api/v1/maintenance/due", nil).Expect(t, 200)
	if due.Str("data.0.asset.id") != asset || !strings.Contains(due.Str("data.0.reason"), "270 hours") {
		t.Fatalf("due: %s", due.Raw)
	}

	env.Do("POST", "/api/v1/assets/"+asset+":maintained", map[string]any{"workOrderId": wos.Str("data.0.id")}).Expect(t, 200)
	if n := len(env.Do("GET", "/api/v1/maintenance/due", nil).Expect(t, 200).Get("data").([]any)); n != 0 {
		t.Fatalf("asset should no longer be due")
	}
	wo := env.Do("GET", "/api/v1/work-orders/"+wos.Str("data.0.id"), nil).Expect(t, 200)
	if wo.Str("status") != "closed" || wo.Get("closedAt") == nil || wo.Get("resolvedAt") == nil {
		t.Fatalf("closed work order: %s", wo.Raw)
	}

	// Repair tickets: location comes from the asset; lifecycle timestamps follow status.
	rep := env.Do("POST", "/api/v1/work-orders", map[string]any{"assetId": asset, "title": "Hydraulic leak", "priority": "urgent"}).Expect(t, 201)
	if rep.Str("locationId") != env.LocationID || rep.Str("kind") != "repair" {
		t.Fatalf("repair: %s", rep.Raw)
	}
	env.Do("POST", "/api/v1/work-orders", map[string]any{"title": "Nowhere"}).Expect(t, 422)
	r := env.Do("PATCH", "/api/v1/work-orders/"+rep.Str("id"), map[string]any{"status": "resolved", "cost": "180.00"}).Expect(t, 200)
	if r.Get("resolvedAt") == nil || r.Get("closedAt") != nil {
		t.Fatalf("resolved: %s", r.Raw)
	}
	r = env.Do("PATCH", "/api/v1/work-orders/"+rep.Str("id"), map[string]any{"status": "in_progress"}).Expect(t, 200)
	if r.Get("resolvedAt") != nil {
		t.Fatalf("reopened: %s", r.Raw)
	}

	env.ProcessWebhooks()
	if got := sortedTypes(env); got != "maintenance.due,work_order.closed,work_order.created,work_order.created" {
		t.Fatalf("events: %s", got)
	}
}

func TestCommunication(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"announcement.published", "recognition.posted"})
	emp := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Sam", "lastName": "Lee", "externalId": "e-1"}).Expect(t, 201).Str("id")

	ann := env.Do("POST", "/api/v1/announcements", map[string]any{"title": "New uniform policy", "body": "Read it.", "requireAck": true,
		"locationIds": []string{env.LocationID}}).Expect(t, 201)
	env.Do("POST", "/api/v1/announcements", map[string]any{"title": "x", "body": "y", "locationIds": []string{"loc_nope"}}).Expect(t, 422)
	id := ann.Str("id")
	env.Do("POST", "/api/v1/announcements/"+id+":acknowledge", map[string]any{"employeeExternalId": "e-1"}).Expect(t, 200)
	acks := env.Do("POST", "/api/v1/announcements/"+id+":acknowledge", map[string]any{"employeeId": emp}).Expect(t, 200)
	if acks.Get("count").(float64) != 1 || acks.Str("data.0.employeeId") != emp {
		t.Fatalf("acks: %s", acks.Raw)
	}
	env.Do("GET", "/api/v1/announcements/"+id+"/acknowledgments", nil).Expect(t, 200)

	env.Do("POST", "/api/v1/calendar-events", map[string]any{"title": "Inspection", "startsAt": "2026-10-02T15:00:00Z", "endsAt": "2026-10-02T14:00:00Z"}).Expect(t, 422)
	ev := env.Do("POST", "/api/v1/calendar-events", map[string]any{"title": "Inspection", "kind": "inspection", "locationId": env.LocationID,
		"startsAt": "2026-10-02T14:00:00Z", "endsAt": "2026-10-02T15:00:00Z"}).Expect(t, 201)
	if n := len(env.Do("GET", "/api/v1/calendar-events?kind=inspection", nil).Expect(t, 200).Get("data").([]any)); n != 1 || ev.Str("kind") != "inspection" {
		t.Fatalf("calendar")
	}

	env.Do("POST", "/api/v1/recognitions", map[string]any{"employeeId": emp, "message": "Great close last night!", "givenBy": "Manager"}).Expect(t, 201)
	if env.Do("GET", "/api/v1/recognitions", nil).Expect(t, 200).Str("data.0.message") != "Great close last night!" {
		t.Fatal("recognitions")
	}

	metric := func(v string) map[string]any {
		return map[string]any{"source": "drive-thru", "metrics": []any{map[string]any{"locationExternalId": "101", "key": "service_time",
			"label": "Avg service time", "value": v, "unit": "s", "target": "180"}}}
	}
	if env.Do("POST", "/api/v1/display-metrics", metric("192")).Expect(t, 202).Str("results.0.status") != "created" {
		t.Fatal("metric create")
	}
	if env.Do("POST", "/api/v1/display-metrics", metric("175")).Expect(t, 202).Str("results.0.status") != "updated" {
		t.Fatal("metric update")
	}
	m := env.Do("GET", "/api/v1/display-metrics", nil).Expect(t, 200)
	if m.Str("data.0.value") != "175" || m.Str("data.0.target") != "180" {
		t.Fatalf("metrics: %s", m.Raw)
	}

	env.ProcessWebhooks()
	if got := sortedTypes(env); got != "announcement.published,recognition.posted" {
		t.Fatalf("events: %s", got)
	}

	// Switching gamification off hides recognitions.
	if _, err := env.App.Features.Disable(context.Background(), "displays.gamification", "test"); err != nil {
		t.Fatal(err)
	}
	env.Do("GET", "/api/v1/recognitions", nil).Expect(t, 404)
}

func TestInsights(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"alert.triggered"})
	ctx := context.Background()
	day := "2026-09-27"
	emp := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Ana", "lastName": "Diaz", "externalId": "e-1", "homeLocationId": env.LocationID}).Expect(t, 201).Str("id")
	env.Do("POST", "/api/v1/employees/"+emp+"/pay-rates", map[string]any{"payType": "hourly", "rate": "20.00", "effectiveFrom": "2026-01-01"}).Expect(t, 201)
	// 8 hours with a 30-minute break = 7.5 worked hours = $150.
	env.Do("POST", "/api/v1/time/punches:batch", map[string]any{"source": "clock", "punches": []any{
		map[string]any{"employeeId": emp, "type": "in", "at": day + "T14:00:00Z"},
		map[string]any{"employeeId": emp, "type": "break_start", "at": day + "T18:00:00Z"},
		map[string]any{"employeeId": emp, "type": "break_end", "at": day + "T18:30:00Z"},
		map[string]any{"employeeId": emp, "type": "out", "at": day + "T22:00:00Z"},
	}}).Expect(t, 202)

	muffin := env.Do("POST", "/api/v1/items", map[string]any{"sku": "MUFFIN", "name": "Muffin"}).Expect(t, 201).Str("id")
	env.Do("POST", "/api/v1/inventory/adjustments", map[string]any{"itemId": muffin, "locationId": env.LocationID, "quantity": "100", "unitCost": "1.00", "reason": "opening"}).Expect(t, 201)
	env.Do("POST", "/api/v1/stock-levels:configure", map[string]any{"itemId": muffin, "locationId": env.LocationID, "parLevel": "150", "reorderPoint": "120"}).Expect(t, 200)
	env.Do("POST", "/api/v1/inventory/waste", map[string]any{"itemId": muffin, "locationId": env.LocationID, "quantity": "4", "reason": "expired"}).Expect(t, 201)
	if _, err := env.Pool.Exec(ctx, `UPDATE stock_movements SET occurred_at = $1 WHERE type = 'waste'`, day+"T20:00:00Z"); err != nil {
		t.Fatal(err)
	}

	env.Do("POST", "/api/v1/sales/transactions:batch", map[string]any{"source": "pos", "transactions": []any{
		map[string]any{"externalId": "t1", "locationExternalId": "101", "occurredAt": day + "T18:00:00Z", "total": "330", "tax": "30",
			"lines": []any{map[string]any{"itemSku": "MUFFIN", "quantity": "10", "unitPrice": "30"}}},
		map[string]any{"externalId": "t2", "locationExternalId": "101", "occurredAt": day + "T19:00:00Z", "total": "220", "tax": "20"},
		map[string]any{"externalId": "t3", "locationExternalId": "101", "occurredAt": day + "T19:30:00Z", "total": "11", "tax": "1", "type": "refund"},
	}}).Expect(t, 202)

	k := env.Do("GET", "/api/v1/kpis?from="+day+"&to="+day, nil).Expect(t, 200)
	checks := map[string]string{"netSales": "490", "averageTicket": "245", "laborHours": "7.5", "laborCost": "150",
		"laborPercent": "30.61", "salesPerLaborHour": "65.33", "wasteCost": "4", "cogs": "10", "cogsPercent": "2.04", "lowStockItems": ""}
	for key, want := range checks {
		if want == "" {
			continue
		}
		if got := k.Str(key); got != want {
			t.Fatalf("%s = %q, want %s: %s", key, got, want, k.Raw)
		}
	}
	if k.Get("transactions").(float64) != 2 || k.Get("lowStockItems").(float64) != 1 {
		t.Fatalf("kpis: %s", k.Raw)
	}
	env.Do("GET", "/api/v1/kpis?from=2026-09-28&to=2026-09-27", nil).Expect(t, 422)
	env.Do("GET", "/api/v1/kpis?locationId=loc_nope", nil).Expect(t, 422)

	// Reports.
	r := env.Do("GET", "/api/v1/reports/sales-by-day?from="+day+"&to="+day, nil).Expect(t, 200)
	if r.Str("rows.0.date") != day || r.Str("rows.0.netSales") != "490" {
		t.Fatalf("sales-by-day: %s", r.Raw)
	}
	r = env.Do("GET", "/api/v1/reports/location-ranking?from="+day+"&to="+day, nil).Expect(t, 200)
	if r.Str("rows.0.name") != "Store 101" || r.Str("rows.0.laborPercent") != "30.61" {
		t.Fatalf("location-ranking: %s", r.Raw)
	}
	r = env.Do("GET", "/api/v1/reports/waste-by-reason?from="+day+"&to="+day, nil).Expect(t, 200)
	if r.Str("rows.0.reason") != "expired" || r.Str("rows.0.cost") != "4.00" {
		t.Fatalf("waste-by-reason: %s", r.Raw)
	}
	csv := env.Do("GET", "/api/v1/reports/stock-valuation?format=csv", nil).Expect(t, 200)
	if !strings.HasPrefix(string(csv.Raw), "itemId,sku,name,locationId,onHand,avgCost,value\n") || !strings.Contains(string(csv.Raw), "MUFFIN,Muffin,"+env.LocationID+",86") {
		t.Fatalf("csv: %s", csv.Raw)
	}
	env.Do("GET", "/api/v1/reports/nope", nil).Expect(t, 404)
	if n := len(env.Do("GET", "/api/v1/reports", nil).Expect(t, 200).Get("data").([]any)); n != len(insights.Reports) {
		t.Fatalf("reports listed: %d", n)
	}

	// Recommendations: reorder muffins and high labor yesterday.
	env.App.Now = func() time.Time { return time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC) }
	rec := env.Do("GET", "/api/v1/recommendations", nil).Expect(t, 200)
	kinds := []string{}
	for _, x := range rec.Get("data").([]any) {
		kinds = append(kinds, x.(map[string]any)["kind"].(string))
	}
	if !slices.Contains(kinds, "reorder") || !slices.Contains(kinds, "high_labor") {
		t.Fatalf("recommendations: %s", rec.Raw)
	}

	// Alert rules fire once per day.
	env.Do("POST", "/api/v1/alert-rules", map[string]any{"kpi": "bogus", "comparator": "gt", "threshold": "1"}).Expect(t, 422)
	rule := env.Do("POST", "/api/v1/alert-rules", map[string]any{"kpi": "laborPercent", "locationId": env.LocationID, "comparator": "gt", "threshold": "25"}).Expect(t, 201)
	if rule.Str("name") != "laborPercent above 25" {
		t.Fatalf("rule: %s", rule.Raw)
	}
	env.Do("POST", "/api/v1/alert-rules", map[string]any{"kpi": "netSales", "comparator": "lt", "threshold": "100"}).Expect(t, 201)
	now := time.Date(2026, 9, 27, 23, 0, 0, 0, time.UTC) // 18:00 in Chicago
	fired, err := insights.EvaluateAlerts(ctx, env.Pool, env.App.Features.IsEnabled, now)
	if err != nil || fired != 1 {
		t.Fatalf("fired %d, %v", fired, err)
	}
	if fired, _ = insights.EvaluateAlerts(ctx, env.Pool, env.App.Features.IsEnabled, now.Add(30*time.Minute)); fired != 0 {
		t.Fatalf("alert fired twice in a day")
	}
	// The all-locations rule uses UTC: a new day with no sales yet is below 100.
	if fired, _ = insights.EvaluateAlerts(ctx, env.Pool, env.App.Features.IsEnabled, now.Add(2*time.Hour)); fired != 1 {
		t.Fatalf("UTC rule should fire on the new day, fired %d", fired)
	}
	env.ProcessWebhooks()
	if got := env.Hooks.Types(); !slices.Equal(got, []string{"alert.triggered", "alert.triggered"}) {
		t.Fatalf("events: %v", got)
	}

	// Disabled features drop out of KPIs.
	if _, err := env.App.Features.Disable(ctx, "time", "test"); err != nil {
		t.Fatal(err)
	}
	k = env.Do("GET", "/api/v1/kpis?from="+day, nil).Expect(t, 200)
	if k.Get("laborCost") != nil || k.Get("laborPercent") != nil || k.Str("netSales") != "490" {
		t.Fatalf("kpis without time: %s", k.Raw)
	}
	env.Do("GET", "/api/v1/reports/labor-by-day", nil).Expect(t, 404)
}

func TestOrganizationAccess(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	roles := env.Do("GET", "/api/v1/roles", nil).Expect(t, 200)
	var owner map[string]any
	for _, r := range roles.Get("data").([]any) {
		if m := r.(map[string]any); m["systemKey"] == "owner" {
			owner = m
		}
	}
	if owner == nil {
		t.Fatalf("owner role missing: %s", roles.Raw)
	}
	users := env.Do("GET", "/api/v1/users", nil).Expect(t, 200)
	if users.Str("data.0.email") != "owner@test" || users.Str("data.0.roleId") != owner["id"] {
		t.Fatalf("users: %s", users.Raw)
	}
	if _, ok := users.Get("data.0").(map[string]any)["passwordHash"]; ok {
		t.Fatal("password hash exposed")
	}
	if n := len(env.Do("GET", "/api/v1/users?status=deactivated", nil).Expect(t, 200).Get("data").([]any)); n != 0 {
		t.Fatalf("status filter: %d", n)
	}
}
