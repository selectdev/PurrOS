package catalog

// Special permission values for routes (mirrors httpx.PermSelf etc.).
const (
	anyone = "anyone" // any signed-in person
	nobody = "-"      // integration keys only: data feeds and batch ingestion
)

// RoutePermissions maps "METHOD /path" to the permission a person needs.
// Integration keys use scopes instead; see Scopes.
var RoutePermissions = map[string]string{
	// Organization
	"GET /org-units": anyone, "GET /locations": anyone, "GET /locations/{id}": anyone,
	"GET /locations/external/{externalId}": anyone, "GET /departments": anyone,
	"POST /locations": "organization.manage", "PATCH /locations/{id}": "organization.manage",
	"PUT /locations/external/{externalId}": "organization.manage", "DELETE /locations/{id}": "organization.manage",
	"GET /org-units/{id}": anyone, "GET /org-units/external/{externalId}": anyone,
	"POST /org-units": "organization.manage", "PATCH /org-units/{id}": "organization.manage",
	"PUT /org-units/external/{externalId}": "organization.manage", "DELETE /org-units/{id}": "organization.manage",
	"GET /departments/{id}": anyone, "GET /departments/external/{externalId}": anyone,
	"POST /departments": "organization.manage", "PATCH /departments/{id}": "organization.manage",
	"PUT /departments/external/{externalId}": "organization.manage", "DELETE /departments/{id}": "organization.manage",
	"GET /roles": "users.read", "GET /users": "users.read",

	// People & HR
	"GET /employees": "employees.read", "GET /employees/{id}": "employees.read",
	"GET /employees/external/{externalId}": "employees.read",
	"POST /employees":                      "employees.write", "PATCH /employees/{id}": "employees.write",
	"PUT /employees/external/{externalId}": "employees.write", "DELETE /employees/{id}": "employees.write",
	"POST /employees/{id}:transfer": "employees.write", "POST /employees/{id}:terminate": "employees.write",
	"POST /employees/{id}:rehire":           "employees.write",
	"GET /employees/{employeeId}/documents": "documents.read", "GET /employees/{employeeId}/documents/{id}": "documents.read",
	"POST /employees/{employeeId}/documents": "documents.manage", "PATCH /employees/{employeeId}/documents/{id}": "documents.manage",
	"DELETE /employees/{employeeId}/documents/{id}": "documents.manage",
	"GET /skills": "employees.read", "GET /skills/{id}": "employees.read", "GET /skills/external/{externalId}": "employees.read",
	"POST /skills": "skills.manage", "PATCH /skills/{id}": "skills.manage", "PUT /skills/external/{externalId}": "skills.manage",
	"DELETE /skills/{id}":        "skills.manage",
	"GET /employees/{id}/skills": "employees.read", "PUT /employees/{id}/skills/{skillId}": "skills.manage",
	"DELETE /employees/{id}/skills/{skillId}": "skills.manage",
	"GET /employees/{id}/pay-rates":           "pay.read", "POST /employees/{id}/pay-rates": "pay.write",
	"GET /payslips": "pay.read", "POST /payslips": nobody,

	// Time & Attendance
	"GET /time/punches": "punches.read", "POST /time/punches:batch": nobody,
	"GET /labor-rule-sets": "timesheets.read", "GET /labor-rule-sets/{id}": "timesheets.read",
	"POST /labor-rule-sets": "labor_rules.manage", "PATCH /labor-rule-sets/{id}": "labor_rules.manage",
	"GET /timesheets": "timesheets.read", "GET /timesheets/{id}": "timesheets.read",
	"POST /timesheets:build": "timesheets.approve", "POST /timesheets/{id}:approve": "timesheets.approve",
	"POST /timesheets/{id}:reject": "timesheets.approve",
	"GET /pay-periods":             "timesheets.read", "GET /pay-periods/{id}": "timesheets.read",
	"POST /pay-periods": "pay_periods.lock", "POST /pay-periods/{id}:lock": "pay_periods.lock",
	"GET /pay-periods/{id}/export": "payroll.export",
	"GET /time-off/types":          anyone, "GET /time-off/types/{id}": anyone, "GET /time-off/types/external/{externalId}": anyone,
	"POST /time-off/types": "settings.manage", "PATCH /time-off/types/{id}": "settings.manage",
	"PUT /time-off/types/external/{externalId}": "settings.manage", "DELETE /time-off/types/{id}": "settings.manage",
	"GET /time-off/requests": "time_off.read", "GET /time-off/balances": "time_off.read",
	"POST /time-off/requests": "time_off.approve", "POST /time-off/requests/{id}:approve": "time_off.approve",
	"POST /time-off/requests/{id}:reject": "time_off.approve", "POST /time-off/requests/{id}:cancel": "time_off.approve",
	"POST /time-off/adjustments": "time_off.approve",

	// Scheduling & Forecasting
	"GET /schedules": "schedules.read", "POST /schedules:publish": "schedules.publish",
	"GET /shifts": "schedules.read", "GET /shifts/{id}": "schedules.read", "GET /shifts/external/{externalId}": "schedules.read",
	"POST /shifts": "schedules.manage", "PATCH /shifts/{id}": "schedules.manage",
	"PUT /shifts/external/{externalId}": "schedules.manage", "POST /shifts/{id}:claim": "schedules.manage",
	"GET /availability": "schedules.read", "GET /availability/{id}": "schedules.read",
	"POST /availability": "schedules.manage", "PATCH /availability/{id}": "schedules.manage",
	"DELETE /availability/{id}": "schedules.manage",
	"GET /shift-swaps":          "schedules.read", "POST /shift-swaps": "schedules.manage",
	"POST /shift-swaps/{id}:approve": "shift_swaps.approve", "POST /shift-swaps/{id}:reject": "shift_swaps.approve",
	"GET /forecasts": "forecasts.read", "POST /forecasts/adjustments": "forecasts.manage", "POST /demand-drivers": nobody,
	"GET /staffing-needs": "schedules.read",
	"GET /staffing-rules": "schedules.read", "GET /staffing-rules/{id}": "schedules.read",
	"POST /staffing-rules": "staffing_rules.manage", "PATCH /staffing-rules/{id}": "staffing_rules.manage",
	"DELETE /staffing-rules/{id}": "staffing_rules.manage",

	// Cash
	"GET /cash/business-days": "cash.read", "GET /cash/counts": "cash.read", "GET /cash/deposits": "cash.read",
	"POST /cash/counts": "cash.count", "POST /cash/deposits": "cash.deposits",
	"POST /cash/business-days/{locationId}/{date}:close": "cash.manage",
	"POST /cash/tenders": nobody, "POST /cash/settlements": nobody, "POST /cash/bank-transactions": nobody,

	// Inventory
	"GET /items": "inventory.read", "GET /items/{id}": "inventory.read", "GET /items/external/{externalId}": "inventory.read",
	"POST /items": "items.manage", "PATCH /items/{id}": "items.manage", "PUT /items/external/{externalId}": "items.manage",
	"GET /stock-levels": "inventory.read", "POST /stock-levels:configure": "items.manage",
	"GET /stock-movements":        "inventory.read",
	"POST /inventory/adjustments": "inventory.adjust", "POST /inventory/waste": "waste.record",
	"GET /stock-counts": "inventory.read", "GET /stock-counts/{id}": "inventory.read",
	"POST /stock-counts": "stock_counts.count", "POST /stock-counts/{id}:post": "stock_counts.manage",
	"POST /stock-counts/{id}:cancel": "stock_counts.manage",
	"GET /transfers":                 "inventory.read", "GET /transfers/{id}": "inventory.read",
	"POST /transfers": "transfers.manage", "POST /transfers/{id}:receive": "transfers.manage",
	"GET /usage-recipes/{itemId}": "inventory.read", "PUT /usage-recipes/{itemId}": "usage_recipes.manage",

	// Purchasing
	"GET /suppliers": "purchasing.read", "GET /suppliers/{id}": "purchasing.read",
	"GET /suppliers/external/{externalId}": "purchasing.read", "GET /suppliers/{id}/catalog": "purchasing.read",
	"POST /suppliers": "suppliers.manage", "PATCH /suppliers/{id}": "suppliers.manage",
	"PUT /suppliers/external/{externalId}": "suppliers.manage", "DELETE /suppliers/{id}": "suppliers.manage",
	"PUT /suppliers/{id}/catalog": "suppliers.manage",
	"GET /suggested-orders":       "orders.create",
	"GET /purchase-orders":        "purchasing.read", "GET /purchase-orders/{id}": "purchasing.read",
	"POST /purchase-orders": "orders.create", "POST /purchase-orders/{id}:send": "orders.create",
	"POST /purchase-orders/{id}:cancel": "orders.create", "POST /purchase-orders/{id}:approve": "purchase_orders.approve",
	"POST /purchase-orders/{id}:receive": "goods_receipts.create",
	"GET /supplier-invoices":             "purchasing.read", "POST /supplier-invoices": "invoices.match",
	"POST /supplier-invoices/{id}:approve": "invoices.match", "POST /supplier-invoices/{id}:dispute": "invoices.match",

	// Sales
	"GET /sales/transactions": "sales.read", "POST /sales/transactions:batch": nobody,
	"GET /sales-summaries": "sales.read", "POST /sales-summaries": nobody,
	"GET /sales/unmapped-items": "sales.read", "POST /sales/unmapped-items:map": "sales.unmapped.resolve",
	"GET /customers": "sales.read", "GET /customers/{id}": "sales.read", "GET /customers/external/{externalId}": "sales.read",
	"POST /customers": "customers.manage", "PATCH /customers/{id}": "customers.manage",
	"PUT /customers/external/{externalId}": "customers.manage", "DELETE /customers/{id}": "customers.manage",
	"GET /sales-orders": "sales.read", "GET /sales-orders/{id}": "sales.read",
	"POST /sales-orders": "sales_orders.manage", "PUT /sales-orders/external/{externalId}": "sales_orders.manage",
	"POST /sales-orders/{id}:ship": "sales_orders.manage", "POST /sales-orders/{id}:cancel": "sales_orders.manage",
	"POST /sales-orders/{id}:invoice": "invoices.issue",
	"GET /invoices":                   "sales.read", "GET /invoices/{id}": "sales.read", "GET /invoices/{id}/pdf": "sales.read",
	"POST /invoices/{id}:pay": "invoices.issue", "POST /invoices/{id}:void": "invoices.issue",

	// Forms, checklists & compliance
	"GET /forms": "checklists.complete", "GET /forms/{id}": "checklists.complete",
	"GET /forms/external/{externalId}": "checklists.complete",
	"POST /forms":                      "forms.manage", "PATCH /forms/{id}": "forms.manage", "PUT /forms/external/{externalId}": "forms.manage",
	"DELETE /forms/{id}":     "forms.manage",
	"POST /form-submissions": "checklists.complete",
	"GET /form-submissions":  "checklists.manage", "GET /form-submissions/{id}": "checklists.manage",
	"GET /audits":             "audits.read",
	"GET /corrective-actions": "corrective_actions.manage", "GET /corrective-actions/{id}": "corrective_actions.manage",
	"POST /corrective-actions": "corrective_actions.manage", "PATCH /corrective-actions/{id}": "corrective_actions.manage",
	"POST /corrective-actions/{id}:close": "corrective_actions.manage",
	"GET /sensors":                        "sensors.manage", "GET /sensors/{id}": "sensors.manage", "GET /sensors/external/{externalId}": "sensors.manage",
	"GET /sensors/{id}/readings": "sensors.manage",
	"POST /sensors":              "sensors.manage", "PATCH /sensors/{id}": "sensors.manage", "PUT /sensors/external/{externalId}": "sensors.manage",
	"DELETE /sensors/{id}": "sensors.manage", "POST /sensor-readings": nobody,

	// Equipment
	"GET /assets": "equipment.read", "GET /assets/{id}": "equipment.read", "GET /assets/external/{externalId}": "equipment.read",
	"GET /maintenance/due": "equipment.read",
	"POST /assets":         "equipment.manage", "PATCH /assets/{id}": "equipment.manage", "PUT /assets/external/{externalId}": "equipment.manage",
	"DELETE /assets/{id}": "equipment.manage", "POST /assets/{id}/meter-readings": "equipment.manage",
	"POST /assets/{id}:maintained": "equipment.manage",
	"GET /work-orders":             "equipment.read", "GET /work-orders/{id}": "equipment.read",
	"GET /work-orders/external/{externalId}": "equipment.read",
	"POST /work-orders":                      "work_orders.create", "PATCH /work-orders/{id}": "work_orders.manage",
	"PUT /work-orders/external/{externalId}": "work_orders.manage",

	// Communication & displays
	"GET /announcements": anyone, "GET /announcements/{id}": anyone,
	"POST /announcements": "announcements.send", "PATCH /announcements/{id}": "announcements.send",
	"DELETE /announcements/{id}":              "announcements.send",
	"GET /announcements/{id}/acknowledgments": "announcements.send", "POST /announcements/{id}:acknowledge": "announcements.send",
	"GET /calendar-events": anyone, "GET /calendar-events/{id}": anyone, "GET /calendar-events/external/{externalId}": anyone,
	"POST /calendar-events": "calendar.manage", "PATCH /calendar-events/{id}": "calendar.manage",
	"PUT /calendar-events/external/{externalId}": "calendar.manage", "DELETE /calendar-events/{id}": "calendar.manage",
	"GET /recognitions": anyone, "POST /recognitions": "recognition.give",
	"GET /display-metrics": anyone, "POST /display-metrics": nobody,

	// Reports & insights
	"GET /kpis": "reports.read", "GET /reports": "reports.read", "GET /reports/{key}": "reports.read",
	"GET /recommendations": "recommendations.read",
	"GET /alert-rules":     "reports.read", "GET /alert-rules/{id}": "reports.read",
	"POST /alert-rules": "alerts.manage", "PATCH /alert-rules/{id}": "alerts.manage", "DELETE /alert-rules/{id}": "alerts.manage",
}

// RoutePermission returns the permission for a route.
func RoutePermission(method, path string) (string, bool) {
	p, ok := RoutePermissions[method+" "+path]
	return p, ok
}
