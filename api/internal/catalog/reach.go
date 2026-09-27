package catalog

// RouteReach gives, for actions on a record by ID, a query returning the
// record's location and employee ($1 is the {id} path parameter), so people
// whose permission reaches only some locations or their own team can use them.
var RouteReach = map[string]string{
	"GET /timesheets/{id}":                    tsReach,
	"POST /timesheets/{id}:approve":           tsReach,
	"POST /timesheets/{id}:reject":            tsReach,
	"POST /time-off/requests/{id}:approve":    torReach,
	"POST /time-off/requests/{id}:reject":     torReach,
	"POST /time-off/requests/{id}:cancel":     torReach,
	"POST /shifts/{id}:claim":                 `SELECT location_id, NULL FROM shifts WHERE id = $1`,
	"POST /shift-swaps/{id}:approve":          swapReach,
	"POST /shift-swaps/{id}:reject":           swapReach,
	"GET /stock-counts/{id}":                  countReach,
	"POST /stock-counts/{id}:post":            countReach,
	"POST /stock-counts/{id}:cancel":          countReach,
	"GET /transfers/{id}":                     `SELECT from_location_id, NULL FROM transfers WHERE id = $1`,
	"POST /transfers/{id}:receive":            `SELECT to_location_id, NULL FROM transfers WHERE id = $1`,
	"GET /purchase-orders/{id}":               poReach,
	"POST /purchase-orders/{id}:approve":      poReach,
	"POST /purchase-orders/{id}:send":         poReach,
	"POST /purchase-orders/{id}:cancel":       poReach,
	"POST /purchase-orders/{id}:receive":      poReach,
	"POST /supplier-invoices/{id}:approve":    supInvReach,
	"POST /supplier-invoices/{id}:dispute":    supInvReach,
	"GET /sales-orders/{id}":                  soReach,
	"POST /sales-orders/{id}:ship":            soReach,
	"POST /sales-orders/{id}:cancel":          soReach,
	"POST /sales-orders/{id}:invoice":         soReach,
	"GET /invoices/{id}":                      invReach,
	"GET /invoices/{id}/pdf":                  invReach,
	"POST /invoices/{id}:pay":                 invReach,
	"POST /invoices/{id}:void":                invReach,
	"GET /form-submissions/{id}":              `SELECT location_id, employee_id FROM form_submissions WHERE id = $1`,
	"POST /corrective-actions/{id}:close":     `SELECT location_id, NULL FROM corrective_actions WHERE id = $1`,
	"GET /sensors/{id}/readings":              `SELECT location_id, NULL FROM sensors WHERE id = $1`,
	"POST /assets/{id}/meter-readings":        assetReach,
	"POST /assets/{id}:maintained":            assetReach,
	"GET /announcements/{id}/acknowledgments": `SELECT NULL, NULL FROM announcements WHERE id = $1`,
}

const (
	tsReach     = `SELECT NULL, employee_id FROM timesheets WHERE id = $1`
	torReach    = `SELECT NULL, employee_id FROM time_off_requests WHERE id = $1`
	swapReach   = `SELECT s.location_id, NULL FROM shift_swap_requests w JOIN shifts s ON s.id = w.shift_id WHERE w.id = $1`
	countReach  = `SELECT location_id, NULL FROM stock_counts WHERE id = $1`
	poReach     = `SELECT location_id, NULL FROM purchase_orders WHERE id = $1`
	supInvReach = `SELECT p.location_id, NULL FROM supplier_invoices i LEFT JOIN purchase_orders p ON p.id = i.po_id WHERE i.id = $1`
	soReach     = `SELECT location_id, NULL FROM sales_orders WHERE id = $1`
	invReach    = `SELECT o.location_id, NULL FROM invoices i LEFT JOIN sales_orders o ON o.id = i.sales_order_id WHERE i.id = $1`
	assetReach  = `SELECT location_id, NULL FROM assets WHERE id = $1`
)
