// Package catalog defines the permission catalog (for people's roles) and the
// API scopes (for integration keys), each tied to the feature it belongs to.
// It mirrors docs/admin/permissions-reference.md and docs/api/README.md#scopes.
package catalog

import "strings"

type Permission struct {
	Key         string `json:"key"`
	Feature     string `json:"feature"`
	Description string `json:"description"`
}

var Permissions = []Permission{
	// Platform (always on)
	{"users.read", "core", "View accounts, their roles and assignments"},
	{"users.manage", "core", "Invite, deactivate and change the role or assignments of accounts"},
	{"roles.manage", "core", "Create, edit and delete roles (Owner-only to grant)"},
	{"organization.manage", "core", "Edit the hierarchy, locations, departments and terminology"},
	{"settings.manage", "core", "Change company settings"},
	{"integrations.manage", "core", "Register, pause, rotate and remove integrations"},
	{"webhooks.manage", "core", "Manage webhook endpoints and view deliveries"},
	{"api_keys.personal", "core", "Create personal API keys"},
	{"audit.read", "core", "View the audit log"},
	{"attachments.read", "core", "View uploaded files (proof, photos, receipts…) within reach"},
	{"attachments.manage", "core", "Delete uploaded files within reach"},

	// People & HR
	{"employees.read", "people", "View employee profiles"},
	{"employees.write", "people", "Create and edit employees, transfers and terminations"},
	{"employees.sensitive.read", "people", "View sensitive employee fields"},
	{"employees.sensitive.write", "people", "Edit sensitive employee fields"},
	{"pay.read", "people", "View pay rates and estimated pay"},
	{"pay.write", "people", "Change pay rates"},
	{"documents.read", "people.documents", "View employee documents"},
	{"documents.manage", "people.documents", "Upload, share and delete employee documents"},
	{"onboarding.manage", "people.onboarding", "Create onboarding checklists and track new hires"},
	{"skills.manage", "people.skills", "Define and assign skills and certifications"},

	// Time & Attendance
	{"punches.read", "time", "View punches"},
	{"punches.correct", "time", "Add or approve punch corrections"},
	{"timesheets.read", "time", "View timesheets"},
	{"timesheets.approve", "time", "Approve or reject timesheets"},
	{"pay_periods.lock", "time", "Final payroll approval and locking of pay periods"},
	{"payroll.export", "time.payroll_export", "Export hours to payroll"},
	{"time_off.read", "time.time_off", "View time-off requests and balances"},
	{"time_off.approve", "time.time_off", "Approve or reject time off"},
	{"labor_rules.manage", "time", "Edit labor rule sets"},
	{"kiosk.manage", "time.kiosk", "Set up kiosk timeclocks and issue clock codes"},

	// Scheduling & Forecasting
	{"schedules.read", "scheduling", "View schedules"},
	{"schedules.manage", "scheduling", "Build and edit schedules"},
	{"schedules.publish", "scheduling", "Publish schedules"},
	{"shift_swaps.approve", "scheduling.shift_swaps", "Approve swaps and open-shift pick-ups"},
	{"forecasts.read", "scheduling.forecasting", "View forecasts"},
	{"forecasts.manage", "scheduling.forecasting", "Adjust forecasts"},
	{"staffing_rules.manage", "scheduling", "Edit staffing rules and minimum coverage"},

	// Cash
	{"cash.read", "cash", "View counts, deposits, over/short and reconciliation"},
	{"cash.count", "cash", "Perform drawer counts, skims and safe drops"},
	{"cash.manage", "cash", "Safe counts, change orders, close the business day"},
	{"cash.deposits", "cash", "Prepare and record bank deposits"},
	{"cash.reconcile", "cash", "Match deposits and settlements, resolve differences"},
	{"cash.paid_outs.approve", "cash.petty_cash", "Approve paid-outs above the limit"},

	// Inventory
	{"inventory.read", "inventory", "View stock, movements, usage and variance"},
	{"items.manage", "inventory", "Create and edit items, units and barcodes"},
	{"usage_recipes.manage", "inventory.usage_recipes", "Edit usage recipes"},
	{"stock_counts.count", "inventory", "Enter counts"},
	{"stock_counts.manage", "inventory", "Schedule, approve and post counts"},
	{"inventory.adjust", "inventory", "Post stock adjustments"},
	{"waste.record", "inventory.waste", "Record waste and shrink"},
	{"transfers.manage", "inventory.transfers", "Send and receive transfers"},

	// Purchasing
	{"purchasing.read", "purchasing", "View suppliers, purchase orders and supplier invoices"},
	{"suppliers.manage", "purchasing", "Manage suppliers, catalogs and order schedules"},
	{"orders.create", "purchasing", "Create orders"},
	{"purchase_orders.approve", "purchasing", "Approve purchase orders above the limit"},
	{"goods_receipts.create", "purchasing", "Receive deliveries"},
	{"invoices.match", "purchasing.invoice_matching", "Match supplier invoices"},

	// Sales
	{"sales.read", "sales", "View sales data and feeds"},
	{"sales.unmapped.resolve", "sales.feeds", "Match unknown items from sales feeds"},
	{"customers.manage", "sales.orders", "Manage customers and price lists"},
	{"sales_orders.manage", "sales.orders", "Create, fulfil and cancel sales orders"},
	{"invoices.issue", "sales.invoicing", "Issue invoices and record payments"},

	// Operations
	{"checklists.complete", "operations", "Fill in assigned checklists and forms"},
	{"checklists.manage", "operations", "Assign and schedule checklists"},
	{"forms.manage", "operations", "Build and edit form templates"},
	{"audits.conduct", "operations.audits", "Carry out scored audits"},
	{"audits.read", "operations.audits", "View audit results"},
	{"corrective_actions.manage", "operations.corrective_actions", "Assign and close corrective actions"},
	{"sensors.manage", "operations.sensors", "Register sensors and set ranges"},

	// Equipment
	{"equipment.read", "equipment", "View assets and maintenance history"},
	{"equipment.manage", "equipment", "Manage the asset register and maintenance plans"},
	{"work_orders.create", "equipment.work_orders", "Report problems"},
	{"work_orders.manage", "equipment.work_orders", "Assign, update and close work orders"},

	// Communication
	{"announcements.send", "communication.announcements", "Send announcements"},
	{"messages.group.manage", "communication.messaging", "Create and manage group chats"},
	{"calendar.manage", "communication.calendar", "Add and edit calendar events"},
	{"files.manage", "communication.files", "Manage the shared files library"},

	// Displays
	{"displays.manage", "displays", "Pair screens and edit display profiles"},
	{"recognition.give", "displays.gamification", "Post shout-outs and run challenges"},

	// Insights
	{"reports.read", "insights", "View dashboards and reports"},
	{"reports.build", "insights.custom_reports", "Create custom reports"},
	{"reports.schedule", "insights.scheduled_reports", "Schedule emailed reports"},
	{"alerts.manage", "insights", "Create KPI alert rules"},
	{"recommendations.read", "insights.recommendations", "See recommended actions"},
	{"ai_assistant.use", "insights.ai_assistant", "Use the AI assistant"},
}

type Scope struct {
	Key         string `json:"key"`
	Feature     string `json:"feature"`
	Description string `json:"description"`
}

var Scopes = []Scope{
	{"organization:read", "core", "Org units, locations, departments, roles, users (read-only)"},
	{"organization:write", "core", "Create and update org units, locations and departments"},
	{"attachments:read", "core", "Read and download uploaded files"},
	{"attachments:write", "core", "Upload and delete files"},
	{"people:read", "people", "Read employees, documents, skills"},
	{"people:write", "people", "Create and update employees"},
	{"people:sensitive", "people", "Sensitive employee fields (needs Owner approval)"},
	{"payroll:read", "people", "Read pay rates, pay period exports, payslips"},
	{"payroll:write", "people", "Write pay rates and payslips"},
	{"time:read", "time", "Read punches, timesheets, time off"},
	{"time:write", "time", "Send punches and time-off requests"},
	{"scheduling:read", "scheduling", "Read forecasts, schedules, shifts"},
	{"scheduling:write", "scheduling", "Send demand drivers, edit shifts"},
	{"cash:read", "cash", "Read counts, deposits, reconciliation"},
	{"cash:write", "cash", "Send tenders, settlements, bank transactions"},
	{"inventory:read", "inventory", "Read items, stock and movements"},
	{"inventory:write", "inventory", "Write items, adjustments, counts"},
	{"purchasing:read", "purchasing", "Read suppliers and purchase orders"},
	{"purchasing:write", "purchasing", "Write suppliers, orders, receipts"},
	{"sales:read", "sales", "Read sales data, customers, orders"},
	{"sales:write", "sales", "Send sales data, write customers and orders"},
	{"operations:read", "operations", "Read forms, submissions, audits"},
	{"operations:write", "operations", "Submit forms, send sensor readings"},
	{"equipment:read", "equipment", "Read assets and work orders"},
	{"equipment:write", "equipment", "Write assets, meter readings, work orders"},
	{"communication:read", "communication", "Read announcements and calendar"},
	{"communication:write", "communication", "Post announcements, events, recognitions"},
	{"reports:read", "insights", "Read reports, KPIs, recommendations"},
	{"reports:write", "insights", "Push custom display metrics, manage alert rules"},
	{"notifications:deliver", "communication", "Deliver notifications by SMS or chat"},
}

// ScopeFeature returns the feature a scope belongs to.
func ScopeFeature(scope string) (string, bool) {
	for _, s := range Scopes {
		if s.Key == scope {
			return s.Feature, true
		}
	}
	return "", false
}

// Events maps each webhook event type to its feature (docs/api/webhooks.md).
var Events = map[string]string{
	"location.created": "core", "location.updated": "core", "location.archived": "core",
	"org_unit.created": "core", "org_unit.updated": "core", "org_unit.archived": "core",
	"department.created": "core", "department.updated": "core", "department.archived": "core",
	"attachment.uploaded": "core", "attachment.deleted": "core",

	"employee.created": "people", "employee.updated": "people", "employee.transferred": "people",
	"employee.terminated": "people", "employee.archived": "people", "pay_rate.changed": "people",
	"document.expiring": "people.documents", "certification.expiring": "people.skills",

	"punch.received": "time", "punch.corrected": "time", "punch.exception": "time",
	"timesheet.approved": "time", "timesheet.rejected": "time", "pay_period.locked": "time",
	"time_off.requested": "time.time_off", "time_off.approved": "time.time_off", "time_off.rejected": "time.time_off",

	"forecast.updated": "scheduling.forecasting", "schedule.published": "scheduling", "shift.changed": "scheduling",
	"shift.swap_requested": "scheduling.shift_swaps", "shift.swap_approved": "scheduling.shift_swaps",
	"open_shift.posted": "scheduling.open_shifts",

	"cash.count_completed": "cash", "cash.over_short_exceeded": "cash", "cash.deposit_recorded": "cash",
	"cash.deposit_mismatch": "cash.deposit_verification", "cash.settlement_mismatch": "cash.tender_reconciliation",
	"cash.business_day_closed": "cash",

	"item.created": "inventory", "item.updated": "inventory", "stock.level_changed": "inventory",
	"stock.below_reorder_point": "inventory", "stock_count.posted": "inventory", "waste.recorded": "inventory.waste",
	"transfer.sent": "inventory.transfers", "transfer.received": "inventory.transfers",
	"transfer.discrepancy": "inventory.transfers",

	"purchase_order.created": "purchasing", "purchase_order.approved": "purchasing", "purchase_order.sent": "purchasing",
	"purchase_order.received": "purchasing", "supplier_invoice.mismatch": "purchasing.invoice_matching",

	"sales.unmapped_item": "sales.feeds", "sales_order.created": "sales.orders", "sales_order.shipped": "sales.orders",
	"sales_order.cancelled": "sales.orders", "invoice.issued": "sales.invoicing", "invoice.paid": "sales.invoicing",

	"form.submitted": "operations", "form.answer_failed": "operations", "checklist.overdue": "operations",
	"corrective_action.created": "operations.corrective_actions", "corrective_action.closed": "operations.corrective_actions",
	"audit.completed": "operations.audits", "sensor.out_of_range": "operations.sensors",

	"maintenance.due": "equipment.maintenance", "work_order.created": "equipment.work_orders",
	"work_order.updated": "equipment.work_orders", "work_order.closed": "equipment.work_orders",

	"announcement.published": "communication.announcements", "recognition.posted": "displays.gamification",
	"notification.requested": "communication",

	"alert.triggered": "insights", "recommendation.created": "insights.recommendations",
}

// PingEvent is sent by POST /webhook-endpoints/{id}:ping. It isn't in Events:
// nobody subscribes to it, it goes only to the endpoint being tested.
const PingEvent = "webhook.ping"

// EventScopeFeature returns the scope feature an integration needs to receive
// an event: the event's top-level feature, except that team display events
// come with the communication scopes and core events with the scope for their
// kind of record.
func EventScopeFeature(event string) (feature string, scope string, ok bool) {
	feat, ok := Events[event]
	if !ok {
		return "", "", false
	}
	if feat == "core" {
		if strings.HasPrefix(event, "attachment.") {
			return "core", "attachments:read", true
		}
		return "core", "organization:read", true
	}
	top, _, _ := strings.Cut(feat, ".")
	if top == "displays" {
		top = "communication"
	}
	return top, "", true
}

// ScopesCoverEvent reports whether an integration with these scopes may
// receive an event: it needs a scope for the event's feature (read or write),
// or for core events the specific read scope.
func ScopesCoverEvent(scopes []string, event string) bool {
	feat, scope, ok := EventScopeFeature(event)
	if !ok {
		return false
	}
	for _, s := range scopes {
		if scope != "" {
			if s == scope {
				return true
			}
			continue
		}
		if f, _ := ScopeFeature(s); f == feat {
			return true
		}
	}
	return false
}

// RouteReachQuery returns the reach query for a route.
func RouteReachQuery(method, path string) (string, bool) {
	q, ok := RouteReach[method+" "+path]
	return q, ok
}
