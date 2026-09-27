package server_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/selectdev/purros/api/internal/testutil"
)

var everyScope = []string{
	"organization:read", "people:read", "people:write", "payroll:read", "payroll:write",
	"time:read", "time:write", "scheduling:read", "scheduling:write", "cash:read", "cash:write",
	"inventory:read", "inventory:write", "purchasing:read", "purchasing:write", "sales:read", "sales:write",
	"operations:read", "operations:write", "equipment:read", "equipment:write",
	"communication:read", "communication:write", "reports:read", "reports:write",
}

func TestPeopleExtras(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	emp := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "Dana", "lastName": "Reyes", "homeLocationId": env.LocationID}).Expect(t, 201).Str("id")

	// Nested documents.
	doc := env.Do("POST", "/api/v1/employees/"+emp+"/documents", map[string]any{"type": "contract", "name": "Contract 2026", "expiresOn": "2027-01-01"}).Expect(t, 201)
	if doc.Str("employeeId") != emp || doc.Str("expiresOn") != "2027-01-01" {
		t.Fatalf("document: %s", doc.Raw)
	}
	if n := len(env.Do("GET", "/api/v1/employees/"+emp+"/documents", nil).Expect(t, 200).Get("data").([]any)); n != 1 {
		t.Fatalf("expected 1 document, got %d", n)
	}
	env.Do("GET", "/api/v1/employees/emp_other/documents/"+doc.Str("id"), nil).Expect(t, 404)
	env.Do("DELETE", "/api/v1/employees/"+emp+"/documents/"+doc.Str("id"), nil).Expect(t, 200)
	env.Do("POST", "/api/v1/employees/emp_nope/documents", map[string]any{"type": "x", "name": "y"}).Expect(t, 404)

	// Skills with validity.
	skill := env.Do("POST", "/api/v1/skills", map[string]any{"name": "First aid", "validDays": 365}).Expect(t, 201).Str("id")
	r := env.Do("PUT", "/api/v1/employees/"+emp+"/skills/"+skill, map[string]any{"obtainedOn": "2026-01-10"}).Expect(t, 200)
	if r.Str("data.0.expiresOn") != "2027-01-10" {
		t.Fatalf("skill expiry: %s", r.Raw)
	}

	// Pay rates are effective-dated.
	env.Do("POST", "/api/v1/employees/"+emp+"/pay-rates", map[string]any{"payType": "hourly", "rate": "18.50", "effectiveFrom": "2026-01-01"}).Expect(t, 201)
	env.Do("POST", "/api/v1/employees/"+emp+"/pay-rates", map[string]any{"payType": "hourly", "rate": "20.00", "effectiveFrom": "2026-09-01"}).Expect(t, 201)
	r = env.Do("GET", "/api/v1/employees/"+emp+"/pay-rates", nil).Expect(t, 200)
	if r.Str("data.0.rate") != "20" || r.Str("data.1.rate") != "18.5" {
		t.Fatalf("pay rates: %s", r.Raw)
	}

	// Payslips ingest idempotently.
	ps := map[string]any{"source": "payroll:acme", "payslips": []any{map[string]any{
		"externalId": "ps-1", "employeeId": emp, "periodStart": "2026-09-01", "periodEnd": "2026-09-14", "gross": "1480.00", "currency": "USD"}}}
	env.Do("POST", "/api/v1/payslips", ps).Expect(t, 202)
	if env.Do("POST", "/api/v1/payslips", ps).Expect(t, 202).Str("results.0.status") != "updated" {
		t.Fatal("payslip resend should update")
	}

	// Transfer and rehire.
	dept := insertDepartment(t, env, "Kitchen")
	r = env.Do("POST", "/api/v1/employees/"+emp+":transfer", map[string]any{"departmentId": dept, "position": "Cook"}).Expect(t, 200)
	if r.Str("departmentId") != dept || r.Str("position") != "Cook" {
		t.Fatalf("transfer: %s", r.Raw)
	}
	env.Do("POST", "/api/v1/employees/"+emp+":rehire", map[string]any{"startDate": "2026-10-01"}).Expect(t, 409)
	env.Do("POST", "/api/v1/employees/"+emp+":terminate", map[string]any{"endDate": "2026-09-30"}).Expect(t, 200)
	if env.Do("POST", "/api/v1/employees/"+emp+":rehire", map[string]any{"startDate": "2026-11-01"}).Expect(t, 200).Str("status") != "active" {
		t.Fatal("rehire should reactivate")
	}
}

func insertDepartment(t *testing.T, env *testutil.Env, name string) string {
	t.Helper()
	id := "dep_" + strings.ReplaceAll(strings.ToLower(name), " ", "")
	if _, err := env.Pool.Exec(t.Context(), `INSERT INTO departments (id, name) VALUES ($1, $2)`, id, name); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTimesheetsAndPayroll(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"timesheet.approved", "pay_period.locked"})
	emp := env.Do("POST", "/api/v1/employees", map[string]any{"externalId": "e1", "firstName": "A", "lastName": "B", "homeLocationId": env.LocationID}).Expect(t, 201).Str("id")
	env.Do("POST", "/api/v1/employees/"+emp+"/pay-rates", map[string]any{"payType": "hourly", "rate": "20", "effectiveFrom": "2026-01-01"}).Expect(t, 201)
	env.Do("POST", "/api/v1/labor-rule-sets", map[string]any{"name": "Standard", "dailyOvertimeMinutes": 480, "weeklyOvertimeMinutes": 2400, "breakRequiredAfterMinutes": 360}).Expect(t, 201)

	// Chicago is UTC-5 in September. Day 1: 08:00–18:00 local with a 30 min break (9.5 h → 1.5 h daily OT).
	// Day 2: 08:00–15:00 with no break (7 h → missing break). Day 3: clock in only.
	punches := []any{
		map[string]any{"employeeId": emp, "type": "in", "at": "2026-09-14T13:00:00Z"},
		map[string]any{"employeeId": emp, "type": "break_start", "at": "2026-09-14T17:00:00Z"},
		map[string]any{"employeeId": emp, "type": "break_end", "at": "2026-09-14T17:30:00Z"},
		map[string]any{"employeeId": emp, "type": "out", "at": "2026-09-14T23:00:00Z"},
		map[string]any{"employeeId": emp, "type": "in", "at": "2026-09-15T13:00:00Z"},
		map[string]any{"employeeId": emp, "type": "out", "at": "2026-09-15T20:00:00Z"},
		map[string]any{"employeeId": emp, "type": "in", "at": "2026-09-16T13:00:00Z"},
	}
	env.Do("POST", "/api/v1/time/punches:batch", map[string]any{"source": "tc", "punches": punches}).Expect(t, 202)

	period := env.Do("POST", "/api/v1/pay-periods", map[string]any{"startDate": "2026-09-14"}).Expect(t, 201)
	if period.Str("endDate") != "2026-09-27" {
		t.Fatalf("default period length: %s", period.Raw)
	}
	env.Do("POST", "/api/v1/pay-periods", map[string]any{"startDate": "2026-09-20"}).Expect(t, 409) // overlap
	pid := period.Str("id")

	r := env.Do("POST", "/api/v1/timesheets:build", map[string]any{"payPeriodId": pid}).Expect(t, 200)
	if r.Get("built").(float64) != 1 {
		t.Fatalf("build: %s", r.Raw)
	}
	ts := env.Do("GET", "/api/v1/timesheets?payPeriodId="+pid, nil).Expect(t, 200)
	if ts.Get("data.0.workedMinutes").(float64) != 990 || ts.Get("data.0.overtimeMinutes").(float64) != 90 || ts.Get("data.0.breakMinutes").(float64) != 30 {
		t.Fatalf("totals: %s", ts.Raw)
	}
	codes := fmt.Sprint(ts.Get("data.0.exceptions"))
	for _, want := range []string{"missing_break", "missing_clock_out", "overtime"} {
		if !strings.Contains(codes, want) {
			t.Fatalf("expected exception %s in %s", want, codes)
		}
	}
	tsID := ts.Str("data.0.id")

	// Can't lock with unapproved timesheets.
	env.Do("POST", "/api/v1/pay-periods/"+pid+":lock", nil).Expect(t, 409)
	env.Do("POST", "/api/v1/timesheets/"+tsID+":approve", map[string]any{"note": "ok"}).Expect(t, 200)
	env.Do("POST", "/api/v1/pay-periods/"+pid+":lock", nil).Expect(t, 200)
	env.Do("POST", "/api/v1/timesheets/"+tsID+":reject", nil).Expect(t, 409)
	env.Do("POST", "/api/v1/timesheets:build", map[string]any{"payPeriodId": pid}).Expect(t, 409)

	// Export: 15 regular h × 20 + 1.5 OT h × 20 × 1.5 = 345.
	r = env.Do("GET", "/api/v1/pay-periods/"+pid+"/export", nil).Expect(t, 200)
	if r.Str("rows.0.regularHours") != "15" || r.Str("rows.0.overtimeHours") != "1.5" || r.Str("rows.0.estimatedGross") != "345" {
		t.Fatalf("export: %s", r.Raw)
	}
	csv := env.Do("GET", "/api/v1/pay-periods/"+pid+"/export?format=csv", nil).Expect(t, 200)
	if !strings.HasPrefix(csv.Header.Get("Content-Type"), "text/csv") || !strings.Contains(string(csv.Raw), "15.00,1.50") {
		t.Fatalf("csv: %s", csv.Raw)
	}
	env.ProcessWebhooks()
	if got := sortedTypes(env); got != "pay_period.locked,timesheet.approved" {
		t.Fatalf("events: %s", got)
	}
}

func TestTimeOff(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	emp := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "A", "lastName": "B"}).Expect(t, 201).Str("id")
	typ := env.Do("POST", "/api/v1/time-off/types", map[string]any{"name": "Vacation", "paid": true}).Expect(t, 201).Str("id")
	env.Do("POST", "/api/v1/time-off/adjustments", map[string]any{"employeeId": emp, "typeId": typ, "hours": "40", "reason": "Annual accrual"}).Expect(t, 201)

	req := env.Do("POST", "/api/v1/time-off/requests", map[string]any{"employeeId": emp, "typeId": typ, "startDate": "2026-10-05", "endDate": "2026-10-06", "hours": "16"}).Expect(t, 201)
	env.Do("POST", "/api/v1/time-off/requests", map[string]any{"employeeId": emp, "typeId": typ, "startDate": "2026-10-06", "endDate": "2026-10-05", "hours": "8"}).Expect(t, 422)
	b := env.Do("GET", "/api/v1/time-off/balances?employeeId="+emp, nil).Expect(t, 200)
	if b.Str("data.0.hours") != "40" || b.Str("data.0.pending") != "16" {
		t.Fatalf("balance before approval: %s", b.Raw)
	}
	env.Do("POST", "/api/v1/time-off/requests/"+req.Str("id")+":approve", nil).Expect(t, 200)
	env.Do("POST", "/api/v1/time-off/requests/"+req.Str("id")+":approve", nil).Expect(t, 409)
	if env.Do("GET", "/api/v1/time-off/balances?employeeId="+emp, nil).Expect(t, 200).Str("data.0.hours") != "24" {
		t.Fatal("approval should deduct 16 hours")
	}
	env.Do("POST", "/api/v1/time-off/requests/"+req.Str("id")+":cancel", nil).Expect(t, 200)
	if env.Do("GET", "/api/v1/time-off/balances?employeeId="+emp, nil).Expect(t, 200).Str("data.0.hours") != "40" {
		t.Fatal("cancelling an approved request should restore the balance")
	}
}

func TestScheduling(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"schedule.published", "open_shift.posted", "shift.swap_approved"})
	a := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "A", "lastName": "A"}).Expect(t, 201).Str("id")
	b := env.Do("POST", "/api/v1/employees", map[string]any{"firstName": "B", "lastName": "B"}).Expect(t, 201).Str("id")
	env.Do("POST", "/api/v1/employees/"+a+"/pay-rates", map[string]any{"payType": "hourly", "rate": "20", "effectiveFrom": "2026-01-01"}).Expect(t, 201)

	s1 := env.Do("POST", "/api/v1/shifts", map[string]any{"locationId": env.LocationID, "employeeId": a,
		"startsAt": "2026-09-28T13:00:00Z", "endsAt": "2026-09-28T21:00:00Z", "breakMinutes": 30}).Expect(t, 201)
	if s1.Str("status") != "draft" {
		t.Fatalf("new shifts are drafts: %s", s1.Raw)
	}
	// Overlap for the same employee.
	env.Do("POST", "/api/v1/shifts", map[string]any{"locationId": env.LocationID, "employeeId": a,
		"startsAt": "2026-09-28T20:00:00Z", "endsAt": "2026-09-28T23:00:00Z"}).Expect(t, 409)
	env.Do("POST", "/api/v1/shifts", map[string]any{"locationId": env.LocationID, "employeeId": a,
		"startsAt": "2026-09-28T20:00:00Z", "endsAt": "2026-09-28T19:00:00Z"}).Expect(t, 422)
	open := env.Do("POST", "/api/v1/shifts", map[string]any{"locationId": env.LocationID,
		"startsAt": "2026-09-29T13:00:00Z", "endsAt": "2026-09-29T17:00:00Z"}).Expect(t, 201).Str("id")

	r := env.Do("POST", "/api/v1/schedules:publish", map[string]any{"locationId": env.LocationID, "from": "2026-09-28T00:00:00Z", "to": "2026-10-05T00:00:00Z"}).Expect(t, 200)
	if r.Get("published").(float64) != 2 || r.Get("openShifts").(float64) != 1 {
		t.Fatalf("publish: %s", r.Raw)
	}
	sched := env.Do("GET", "/api/v1/schedules?locationId="+env.LocationID+"&from=2026-09-28T00:00:00Z&to=2026-10-05T00:00:00Z", nil).Expect(t, 200)
	if sched.Str("totalHours") != "11.5" || sched.Str("estimatedCost") != "150" || sched.Get("openShifts").(float64) != 1 {
		t.Fatalf("schedule: %s", sched.Raw)
	}
	env.Do("POST", "/api/v1/shifts/"+open+":claim", map[string]any{"employeeId": b}).Expect(t, 200)
	env.Do("POST", "/api/v1/shifts/"+open+":claim", map[string]any{"employeeId": a}).Expect(t, 409)

	swap := env.Do("POST", "/api/v1/shift-swaps", map[string]any{"shiftId": s1.Str("id"), "toEmployeeId": b}).Expect(t, 201).Str("id")
	env.Do("POST", "/api/v1/shift-swaps/"+swap+":approve", nil).Expect(t, 200)
	if env.Do("GET", "/api/v1/shifts/"+s1.Str("id"), nil).Expect(t, 200).Str("employeeId") != b {
		t.Fatal("approved swap should reassign the shift")
	}

	// Availability.
	env.Do("POST", "/api/v1/availability", map[string]any{"employeeId": a, "weekday": 1, "startTime": "09:00", "endTime": "17:00"}).Expect(t, 201)
	env.Do("POST", "/api/v1/availability", map[string]any{"employeeId": a, "weekday": 1, "startTime": "17:00", "endTime": "09:00"}).Expect(t, 422)
	r = env.Do("GET", "/api/v1/availability?employeeId="+a, nil).Expect(t, 200)
	if r.Str("data.0.startTime") != "09:00" {
		t.Fatalf("availability: %s", r.Raw)
	}

	env.ProcessWebhooks()
	got := strings.Join(env.Hooks.Types(), ",")
	for _, want := range []string{"open_shift.posted", "schedule.published", "shift.swap_approved"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func TestForecastAndStaffing(t *testing.T) {
	env := testutil.New(t, everyScope, nil)
	// Four Mondays of history at 12:00 local (17:00 UTC): 100, 200, 300, 400 in sales.
	var txns []any
	start := time.Date(2026, 8, 31, 17, 15, 0, 0, time.UTC) // Monday
	for w, amount := range []string{"100", "200", "300", "400"} {
		at := start.AddDate(0, 0, 7*w)
		txns = append(txns, map[string]any{"externalId": fmt.Sprintf("t%d", w), "locationExternalId": "101",
			"occurredAt": at.Format(time.RFC3339), "total": amount})
	}
	env.Do("POST", "/api/v1/sales/transactions:batch", map[string]any{"source": "pos", "transactions": txns}).Expect(t, 202)

	// Forecast for Monday 2026-09-28: average of 400, 300, 200, 100 = 250.
	r := env.Do("GET", "/api/v1/forecasts?locationId="+env.LocationID+"&from=2026-09-28&to=2026-09-28", nil).Expect(t, 200)
	if r.Str("points.0.value") != "250" {
		t.Fatalf("daily forecast: %s", r.Raw)
	}
	env.Do("POST", "/api/v1/forecasts/adjustments", map[string]any{"locationId": env.LocationID, "driver": "sales", "date": "2026-09-28", "percent": "20"}).Expect(t, 201)
	r = env.Do("GET", "/api/v1/forecasts?locationId="+env.LocationID+"&from=2026-09-28&to=2026-09-28&interval=hour", nil).Expect(t, 200)
	if r.Str("points.12.value") != "300" {
		t.Fatalf("hourly adjusted forecast at 12:00: %s", r.Get("points.12"))
	}

	// 1 staff per 100 sales, at least 1 all day.
	env.Do("POST", "/api/v1/staffing-rules", map[string]any{"locationId": env.LocationID, "driver": "sales", "unitsPerStaff": "100"}).Expect(t, 201)
	env.Do("POST", "/api/v1/staffing-rules", map[string]any{"locationId": env.LocationID, "minStaff": 1}).Expect(t, 201)
	env.Do("POST", "/api/v1/staffing-rules", map[string]any{"locationId": env.LocationID, "driver": "sales"}).Expect(t, 422)
	r = env.Do("GET", "/api/v1/staffing-needs?locationId="+env.LocationID+"&date=2026-09-28", nil).Expect(t, 200)
	if r.Get("hours.12.needed").(float64) != 4 || r.Get("hours.3.needed").(float64) != 1 {
		t.Fatalf("needs: noon=%v 3am=%v", r.Get("hours.12"), r.Get("hours.3"))
	}
}
