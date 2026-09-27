// Package features is the registry of switchable features and the store that
// records which are enabled. A disabled feature must leave no trace: its
// routes return 404 feature_disabled, its events are not delivered, and its
// permissions and scopes disappear.
package features

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Core is the always-on platform core. It is not a switchable feature.
const Core = "core"

type Feature struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// Parent returns the parent key of a sub-feature ("time.kiosk" → "time").
func (f Feature) Parent() string {
	if i := strings.IndexByte(f.Key, '.'); i > 0 {
		return f.Key[:i]
	}
	return ""
}

// Registry lists every feature and sub-feature, matching docs/admin/feature-switches.md.
var Registry = []Feature{
	{Key: "people", Name: "People & HR"},
	{Key: "people.documents", Name: "Employee documents"},
	{Key: "people.onboarding", Name: "Onboarding"},
	{Key: "people.skills", Name: "Skills & certifications"},
	{Key: "people.custom_fields", Name: "Custom fields"},

	{Key: "time", Name: "Time & Attendance", DependsOn: []string{"people"}},
	{Key: "time.kiosk", Name: "Kiosk timeclock"},
	{Key: "time.kiosk_photo", Name: "Photo on clock-in", DependsOn: []string{"time.kiosk"}},
	{Key: "time.break_attestation", Name: "Break attestation"},
	{Key: "time.time_off", Name: "Time off"},
	{Key: "time.payroll_export", Name: "Payroll export"},

	{Key: "scheduling", Name: "Scheduling & Forecasting", DependsOn: []string{"people", "time"}},
	{Key: "scheduling.forecasting", Name: "Demand forecasting"},
	{Key: "scheduling.auto_builder", Name: "Automatic schedule builder"},
	{Key: "scheduling.shift_swaps", Name: "Shift swaps"},
	{Key: "scheduling.open_shifts", Name: "Open shifts"},
	{Key: "scheduling.schedule_enforcement", Name: "Schedule-aware punching"},

	{Key: "cash", Name: "Cash Management", DependsOn: []string{"sales"}},
	{Key: "cash.deposit_verification", Name: "Deposit verification"},
	{Key: "cash.tender_reconciliation", Name: "Tender reconciliation"},
	{Key: "cash.petty_cash", Name: "Paid-outs & petty cash"},

	{Key: "inventory", Name: "Inventory"},
	{Key: "inventory.waste", Name: "Waste & shrink"},
	{Key: "inventory.transfers", Name: "Transfers"},
	{Key: "inventory.usage_recipes", Name: "Usage recipes", DependsOn: []string{"sales"}},
	{Key: "inventory.batches", Name: "Batch & expiry tracking"},

	{Key: "purchasing", Name: "Purchasing & Ordering", DependsOn: []string{"inventory"}},
	{Key: "purchasing.suggested_orders", Name: "Suggested orders"},
	{Key: "purchasing.invoice_matching", Name: "Invoice matching"},

	{Key: "sales", Name: "Sales"},
	{Key: "sales.feeds", Name: "Sales feeds"},
	{Key: "sales.orders", Name: "Sales orders"},
	{Key: "sales.invoicing", Name: "Invoicing", DependsOn: []string{"sales.orders"}},

	{Key: "operations", Name: "Forms, Checklists & Compliance"},
	{Key: "operations.audits", Name: "Audits & inspections"},
	{Key: "operations.corrective_actions", Name: "Corrective actions"},
	{Key: "operations.sensors", Name: "Sensor readings"},

	{Key: "equipment", Name: "Equipment & Assets"},
	{Key: "equipment.maintenance", Name: "Preventive maintenance"},
	{Key: "equipment.work_orders", Name: "Repair tickets"},

	{Key: "communication", Name: "Communication"},
	{Key: "communication.announcements", Name: "Announcements"},
	{Key: "communication.messaging", Name: "Messaging"},
	{Key: "communication.direct_messages", Name: "Direct messages", DependsOn: []string{"communication.messaging"}},
	{Key: "communication.calendar", Name: "Company calendar"},
	{Key: "communication.files", Name: "Shared files & links"},

	{Key: "displays", Name: "Team Displays"},
	{Key: "displays.gamification", Name: "Gamification"},
	{Key: "displays.celebrations", Name: "Celebrations"},

	{Key: "insights", Name: "Reports & Insights"},
	{Key: "insights.custom_reports", Name: "Custom reports"},
	{Key: "insights.scheduled_reports", Name: "Scheduled reports"},
	{Key: "insights.recommendations", Name: "Recommended actions"},
	{Key: "insights.ai_assistant", Name: "AI assistant"},

	{Key: "employee_area", Name: "Employee Area", DependsOn: []string{"people"}},
	{Key: "employee_area.estimated_pay", Name: "Estimated pay"},
	{Key: "employee_area.payslips", Name: "Payslips"},
	{Key: "employee_area.profile_edit", Name: "Profile editing"},
	{Key: "employee_area.data_export", Name: "Export my data"},
}

var byKey = func() map[string]Feature {
	m := make(map[string]Feature, len(Registry))
	for _, f := range Registry {
		m[f.Key] = f
	}
	return m
}()

// Lookup returns a feature by key.
func Lookup(key string) (Feature, bool) {
	f, ok := byKey[key]
	return f, ok
}

// requirements returns everything key needs to be on: its parent chain and
// declared dependencies, recursively.
func requirements(key string) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(k string) {
		f, ok := byKey[k]
		if !ok || seen[k] {
			return
		}
		seen[k] = true
		if p := f.Parent(); p != "" {
			walk(p)
		}
		for _, d := range f.DependsOn {
			walk(d)
		}
	}
	walk(key)
	delete(seen, key)
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Dependents returns every feature that directly or indirectly needs key.
func Dependents(key string) []string {
	var out []string
	for _, f := range Registry {
		if f.Key != key && slices.Contains(requirements(f.Key), key) {
			out = append(out, f.Key)
		}
	}
	return out
}

// Store reads and writes feature settings. Features without a stored setting
// are enabled, so a fresh install has everything on until the Owner (or
// first-run setup) switches things off.
type Store struct {
	pool *pgxpool.Pool
	ttl  time.Duration

	mu       sync.Mutex
	cached   map[string]bool
	cachedAt time.Time
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, ttl: 5 * time.Second}
}

func (s *Store) settings(ctx context.Context) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && time.Since(s.cachedAt) < s.ttl {
		return s.cached, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT key, enabled FROM feature_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var k string
		var v bool
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	s.cached, s.cachedAt = m, time.Now()
	return m, rows.Err()
}

// Invalidate drops the cache so the next check reads the database.
func (s *Store) Invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

// IsEnabled reports whether key and everything it needs are enabled.
// The core is always enabled.
func (s *Store) IsEnabled(ctx context.Context, key string) (bool, error) {
	if key == "" || key == Core {
		return true, nil
	}
	if _, ok := byKey[key]; !ok {
		return false, fmt.Errorf("unknown feature %q", key)
	}
	m, err := s.settings(ctx)
	if err != nil {
		return false, err
	}
	for _, k := range append(requirements(key), key) {
		if v, ok := m[k]; ok && !v {
			return false, nil
		}
	}
	return true, nil
}

// Enabled returns every enabled feature key.
func (s *Store) Enabled(ctx context.Context) ([]string, error) {
	var out []string
	for _, f := range Registry {
		ok, err := s.IsEnabled(ctx, f.Key)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, f.Key)
		}
	}
	return out, nil
}

// Enable switches key on, together with everything it needs. It returns the
// keys that were switched on.
func (s *Store) Enable(ctx context.Context, key, actor string) ([]string, error) {
	if _, ok := byKey[key]; !ok {
		return nil, fmt.Errorf("unknown feature %q", key)
	}
	keys := append(requirements(key), key)
	return keys, s.set(ctx, keys, true, actor)
}

// Disable switches key off. Every feature that depends on it is then off as
// well (IsEnabled checks requirements), and comes back when key is switched on
// again. It returns key plus the dependents that were on and are now off.
func (s *Store) Disable(ctx context.Context, key, actor string) ([]string, error) {
	if _, ok := byKey[key]; !ok {
		return nil, fmt.Errorf("unknown feature %q", key)
	}
	affected := []string{key}
	for _, d := range Dependents(key) {
		on, err := s.IsEnabled(ctx, d)
		if err != nil {
			return nil, err
		}
		if on {
			affected = append(affected, d)
		}
	}
	return affected, s.set(ctx, []string{key}, false, actor)
}

func (s *Store) set(ctx context.Context, keys []string, enabled bool, actor string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO feature_settings (key, enabled, changed_by, changed_at)
		SELECT k, $2, $3, now() FROM unnest($1::text[]) AS k
		ON CONFLICT (key) DO UPDATE SET enabled = EXCLUDED.enabled,
			changed_by = EXCLUDED.changed_by, changed_at = EXCLUDED.changed_at`,
		keys, enabled, actor)
	if err == nil {
		_, err = s.pool.Exec(ctx, `SELECT pg_notify($1, '')`, notifyChannel)
	}
	s.Invalidate()
	return err
}

const notifyChannel = "purros_features"

// Listen clears the cache whenever any process changes a feature setting, so
// switches take effect immediately across API and worker instances. It blocks
// until ctx is cancelled, reconnecting on errors.
func (s *Store) Listen(ctx context.Context) {
	for ctx.Err() == nil {
		conn, err := s.pool.Acquire(ctx)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if _, err = conn.Exec(ctx, "LISTEN "+notifyChannel); err == nil {
			for {
				if _, err = conn.Conn().WaitForNotification(ctx); err != nil {
					break
				}
				s.Invalidate()
			}
		}
		conn.Release()
		s.Invalidate()
		if ctx.Err() == nil {
			time.Sleep(time.Second)
		}
	}
}
