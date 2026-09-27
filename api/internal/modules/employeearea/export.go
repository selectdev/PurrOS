package employeearea

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/httpx"
)

// exportQueries are the tables in "Export my data", each limited to the
// employee ($1). Columns are exported as stored.
var exportQueries = []struct{ file, sql string }{
	{"punches.json", `SELECT id, type, at, location_id, source, voided_at, created_at FROM punches WHERE employee_id = $1 ORDER BY at`},
	{"punch-corrections.json", `SELECT id, punch_id, type, at, location_id, reason, status, decision_note, decided_at, created_at FROM punch_corrections WHERE employee_id = $1 ORDER BY created_at`},
	{"timesheets.json", `SELECT id, pay_period_id, status, worked_minutes, regular_minutes, overtime_minutes, break_minutes, days, exceptions, approved_at FROM timesheets WHERE employee_id = $1 ORDER BY created_at`},
	{"time-off-requests.json", `SELECT * FROM time_off_requests WHERE employee_id = $1 ORDER BY created_at`},
	{"time-off-ledger.json", `SELECT * FROM time_off_ledger WHERE employee_id = $1`},
	{"pay-rates.json", `SELECT pay_type, rate, currency, effective_from FROM pay_rates WHERE employee_id = $1 ORDER BY effective_from`},
	{"payslips.json", `SELECT period_start, period_end, pay_date, gross, net, currency, url FROM payslips WHERE employee_id = $1 ORDER BY period_start`},
	{"shifts.json", `SELECT id, location_id, position, starts_at, ends_at, break_minutes, status FROM shifts WHERE employee_id = $1 AND status = 'published' ORDER BY starts_at`},
	{"availability.json", `SELECT weekday, start_time, end_time, kind FROM availability WHERE employee_id = $1 AND archived_at IS NULL`},
	{"documents.json", `SELECT type, name, url, expires_on, created_at FROM employee_documents WHERE employee_id = $1 AND shared_with_employee`},
	{"skills.json", `SELECT s.name, es.* FROM employee_skills es JOIN skills s ON s.id = es.skill_id WHERE es.employee_id = $1`},
	{"history.json", `SELECT created_at, coalesce(nullif(actor_name, ''), actor_type) AS actor, action, after AS changes FROM audit_log WHERE entity_type = 'employee' AND entity_id = $1 ORDER BY created_at`},
}

func exportMyData(c *httpx.Ctx, emp string) (any, error) {
	if err := c.RequireFeature("employee_area.data_export"); err != nil {
		return nil, err
	}
	if err := c.RateLimit("export:"+emp, 5, time.Hour); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		w, err := zw.Create(name)
		if err == nil {
			_, err = w.Write(data)
		}
		return err
	}
	err := pgx.BeginTxFunc(c, c.App.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		profile, err := loadProfile(c, tx, emp)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(profile, "", "  ")
		if err := add("profile.json", b); err != nil {
			return err
		}
		for _, q := range exportQueries {
			var data []byte
			if err := tx.QueryRow(c, `SELECT coalesce(jsonb_pretty(jsonb_agg(to_jsonb(t))), '[]') FROM (`+q.sql+`) t`, emp).Scan(&data); err != nil {
				return err
			}
			if err := add(q.file, data); err != nil {
				return err
			}
		}
		// A spreadsheet-friendly copy of punches.
		rows, err := tx.Query(c, `SELECT at, type, coalesce(location_id, ''), source, voided_at IS NOT NULL FROM punches WHERE employee_id = $1 ORDER BY at`, emp)
		if err != nil {
			return err
		}
		var csvBuf bytes.Buffer
		w := csv.NewWriter(&csvBuf)
		_ = w.Write([]string{"at", "type", "location_id", "source", "voided"})
		for rows.Next() {
			var at time.Time
			var typ, loc, src string
			var voided bool
			if err := rows.Scan(&at, &typ, &loc, &src, &voided); err != nil {
				rows.Close()
				return err
			}
			_ = w.Write([]string{at.UTC().Format(time.RFC3339), typ, loc, src, map[bool]string{true: "yes", false: "no"}[voided]})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		w.Flush()
		return add("punches.csv", csvBuf.Bytes())
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if err := c.Record(c.App.Pool, httpx.Change{Action: "employee.data_exported", EntityType: "employee", EntityID: emp}); err != nil {
		return nil, err
	}
	return httpx.Raw{ContentType: "application/zip", Filename: "my-data-" + c.App.Now().Format("2006-01-02") + ".zip", Body: buf.Bytes()}, nil
}

// Routes returns the Employee Area routes.
func Routes() []httpx.Route {
	var out []httpx.Route
	out = append(out, profileRoutes()...)
	out = append(out, workRoutes()...)
	out = append(out, me("GET", "/export", "Download everything PurrOS stores about you (ZIP of JSON and CSV)", exportMyData))
	return out
}
