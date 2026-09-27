package server_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/selectdev/purros/api/internal/cli"
	"github.com/selectdev/purros/api/internal/testutil"
)

// sortedTypes returns received event types sorted: only events about the same
// record are guaranteed to arrive in order.
func sortedTypes(env *testutil.Env) string {
	types := env.Hooks.Types()
	sort.Strings(types)
	return strings.Join(types, ",")
}

func level(t *testing.T, env *testutil.Env, item, loc string) testutil.Response {
	t.Helper()
	return env.Do("GET", "/api/v1/stock-levels?itemId="+item+"&locationId="+loc, nil).Expect(t, 200)
}

func onHand(t *testing.T, env *testutil.Env, item, loc string) string {
	t.Helper()
	r := level(t, env, item, loc)
	if r.Get("data.0") == nil {
		return "0"
	}
	return r.Str("data.0.onHand")
}

func TestStockLedgerAndSales(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"stock.below_reorder_point", "waste.recorded", "stock_count.posted"})
	beans := env.Do("POST", "/api/v1/items", map[string]any{"sku": "BEANS", "name": "Coffee beans (g)", "baseUnit": "g"}).Expect(t, 201).Str("id")
	milk := env.Do("POST", "/api/v1/items", map[string]any{"sku": "MILK", "name": "Milk (ml)", "baseUnit": "ml"}).Expect(t, 201).Str("id")
	latte := env.Do("POST", "/api/v1/items", map[string]any{"sku": "LATTE", "name": "Latte", "trackStock": false}).Expect(t, 201).Str("id")
	muffin := env.Do("POST", "/api/v1/items", map[string]any{"sku": "MUFFIN", "name": "Muffin", "defaultCost": "0.80"}).Expect(t, 201).Str("id")

	// Receive stock with cost via adjustments.
	env.Do("POST", "/api/v1/inventory/adjustments", map[string]any{"itemId": beans, "locationId": env.LocationID, "quantity": "1000", "unitCost": "0.02", "reason": "opening"}).Expect(t, 201)
	env.Do("POST", "/api/v1/inventory/adjustments", map[string]any{"itemId": milk, "locationId": env.LocationID, "quantity": "5000", "unitCost": "0.001", "reason": "opening"}).Expect(t, 201)
	env.Do("POST", "/api/v1/inventory/adjustments", map[string]any{"itemSku": "MUFFIN", "locationExternalId": "101", "quantity": "10", "reason": "opening"}).Expect(t, 201)
	env.Do("POST", "/api/v1/inventory/adjustments", map[string]any{"itemId": beans, "locationId": env.LocationID, "quantity": "0", "reason": "x"}).Expect(t, 422)
	env.Do("POST", "/api/v1/stock-levels:configure", map[string]any{"itemId": beans, "locationId": env.LocationID, "parLevel": "1000", "reorderPoint": "990"}).Expect(t, 200)

	// A latte uses 18 g beans and 250 ml milk.
	r := env.Do("PUT", "/api/v1/usage-recipes/"+latte, map[string]any{"components": []any{
		map[string]any{"itemSku": "BEANS", "quantity": "18"}, map[string]any{"itemId": milk, "quantity": "250"}}}).Expect(t, 200)
	if len(r.Get("components").([]any)) != 2 {
		t.Fatalf("recipe: %s", r.Raw)
	}
	env.Do("PUT", "/api/v1/usage-recipes/"+latte, map[string]any{"components": []any{map[string]any{"itemId": latte, "quantity": "1"}}}).Expect(t, 422)
	env.Do("PUT", "/api/v1/usage-recipes/"+latte, map[string]any{"components": []any{
		map[string]any{"itemSku": "BEANS", "quantity": "18"}, map[string]any{"itemId": milk, "quantity": "250"}}}).Expect(t, 200)

	sale := func(qtyLatte, qtyMuffin, status string) map[string]any {
		return map[string]any{"source": "pos", "transactions": []any{map[string]any{
			"externalId": "t1", "locationExternalId": "101", "occurredAt": "2026-09-27T15:00:00Z", "status": status,
			"lines": []any{
				map[string]any{"itemSku": "LATTE", "quantity": qtyLatte, "unitPrice": "4.50"},
				map[string]any{"itemSku": "MUFFIN", "quantity": qtyMuffin, "unitPrice": "3.00"},
			}, "total": "12.00"}}}
	}
	env.Do("POST", "/api/v1/sales/transactions:batch", sale("2", "1", "completed")).Expect(t, 202)
	if onHand(t, env, beans, env.LocationID) != "964" || onHand(t, env, milk, env.LocationID) != "4500" || onHand(t, env, muffin, env.LocationID) != "9" {
		t.Fatalf("after sale: beans=%s milk=%s muffin=%s", onHand(t, env, beans, env.LocationID), onHand(t, env, milk, env.LocationID), onHand(t, env, muffin, env.LocationID))
	}
	if onHand(t, env, latte, env.LocationID) != "0" {
		t.Fatal("non-stock items shouldn't be deducted")
	}
	// Re-sending with a different quantity replaces the effect instead of adding to it.
	env.Do("POST", "/api/v1/sales/transactions:batch", sale("1", "1", "completed")).Expect(t, 202)
	if onHand(t, env, beans, env.LocationID) != "982" {
		t.Fatalf("resend should replace, beans=%s", onHand(t, env, beans, env.LocationID))
	}
	// Voiding restores stock.
	env.Do("POST", "/api/v1/sales/transactions:batch", sale("1", "1", "voided")).Expect(t, 202)
	if onHand(t, env, beans, env.LocationID) != "1000" || onHand(t, env, muffin, env.LocationID) != "10" {
		t.Fatal("void should restore stock")
	}

	// Waste and reorder alert.
	env.Do("POST", "/api/v1/inventory/waste", map[string]any{"itemId": beans, "locationId": env.LocationID, "quantity": "20", "reason": "spoilage"}).Expect(t, 201)
	env.Do("POST", "/api/v1/inventory/waste", map[string]any{"itemId": beans, "locationId": env.LocationID, "quantity": "5", "reason": "lost"}).Expect(t, 422)
	if r := env.Do("GET", "/api/v1/stock-levels?belowReorderPoint=true", nil).Expect(t, 200); r.Str("data.0.itemId") != beans {
		t.Fatalf("below reorder point: %s", r.Raw)
	}
	lv := level(t, env, beans, env.LocationID)
	if lv.Str("data.0.avgCost") != "0.02" || lv.Str("data.0.value") != "19.6" {
		t.Fatalf("valuation: %s", lv.Raw)
	}

	// Stock count posts the difference against on-hand at posting time.
	cnt := env.Do("POST", "/api/v1/stock-counts", map[string]any{"locationId": env.LocationID, "lines": []any{map[string]any{"itemId": beans, "counted": "970"}}}).Expect(t, 201)
	if cnt.Str("lines.0.variance") != "-10" {
		t.Fatalf("count variance: %s", cnt.Raw)
	}
	env.Do("POST", "/api/v1/stock-counts/"+cnt.Str("id")+":post", nil).Expect(t, 200)
	env.Do("POST", "/api/v1/stock-counts/"+cnt.Str("id")+":post", nil).Expect(t, 409)
	if onHand(t, env, beans, env.LocationID) != "970" {
		t.Fatalf("after count: %s", onHand(t, env, beans, env.LocationID))
	}

	// Ledger always equals the level.
	var ledger, lvl string
	_ = env.Pool.QueryRow(context.Background(), `SELECT sum(quantity)::text FROM stock_movements WHERE item_id=$1`, beans).Scan(&ledger)
	_ = env.Pool.QueryRow(context.Background(), `SELECT on_hand::text FROM stock_levels WHERE item_id=$1`, beans).Scan(&lvl)
	if strings.TrimRight(strings.TrimRight(ledger, "0"), ".") != strings.TrimRight(strings.TrimRight(lvl, "0"), ".") {
		t.Fatalf("ledger %s != level %s", ledger, lvl)
	}

	// Transfers between locations, with a discrepancy.
	loc2, err := cli.CreateLocation(context.Background(), env.Pool, cli.LocationInput{Name: "Store 102", Timezone: "UTC", Currency: "USD", Cutoff: "00:00"})
	if err != nil {
		t.Fatal(err)
	}
	tr := env.Do("POST", "/api/v1/transfers", map[string]any{"fromLocationId": env.LocationID, "toLocationId": loc2,
		"lines": []any{map[string]any{"itemId": beans, "quantity": "100"}}}).Expect(t, 201)
	env.Do("POST", "/api/v1/transfers", map[string]any{"fromLocationId": env.LocationID, "toLocationId": env.LocationID,
		"lines": []any{map[string]any{"itemId": beans, "quantity": "1"}}}).Expect(t, 422)
	env.Do("POST", "/api/v1/transfers/"+tr.Str("id")+":receive", map[string]any{"lines": []any{map[string]any{"itemId": beans, "receivedQty": "95"}}}).Expect(t, 200)
	if onHand(t, env, beans, env.LocationID) != "870" || onHand(t, env, beans, loc2) != "95" {
		t.Fatalf("transfer: from=%s to=%s", onHand(t, env, beans, env.LocationID), onHand(t, env, beans, loc2))
	}
	if level(t, env, beans, loc2).Str("data.0.avgCost") != "0.02" {
		t.Fatal("transferred stock keeps its cost")
	}

	env.ProcessWebhooks()
	got := strings.Join(env.Hooks.Types(), ",")
	for _, want := range []string{"stock.below_reorder_point", "waste.recorded", "stock_count.posted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func TestPurchasing(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"purchase_order.sent", "purchase_order.received", "supplier_invoice.mismatch"})
	if _, err := env.Pool.Exec(context.Background(), `UPDATE company SET po_approval_limit = 100`); err != nil {
		t.Fatal(err)
	}
	flour := env.Do("POST", "/api/v1/items", map[string]any{"sku": "FLOUR", "name": "Flour (kg)"}).Expect(t, 201).Str("id")
	sup := env.Do("POST", "/api/v1/suppliers", map[string]any{"name": "Mill Co", "leadTimeDays": 2, "orderDays": []int{1, 4}, "externalId": "mill"}).Expect(t, 201).Str("id")
	env.Do("PUT", "/api/v1/suppliers/"+sup+"/catalog", map[string]any{"items": []any{
		map[string]any{"itemSku": "FLOUR", "packSize": "25", "price": "30.00"}}}).Expect(t, 200)

	// Suggested order: par 60, nothing on hand → 60 kg → 3 packs of 25.
	env.Do("POST", "/api/v1/stock-levels:configure", map[string]any{"itemId": flour, "locationId": env.LocationID, "parLevel": "60"}).Expect(t, 200)
	s := env.Do("GET", "/api/v1/suggested-orders?supplierId="+sup+"&locationId="+env.LocationID, nil).Expect(t, 200)
	if s.Str("lines.0.packs") != "3" || s.Str("lines.0.suggestedQty") != "75" || s.Str("total") != "90" {
		t.Fatalf("suggestion: %s", s.Raw)
	}

	// 75 kg × 1.20 = 90 → under the limit, approved immediately.
	po := env.Do("POST", "/api/v1/purchase-orders", map[string]any{"supplierId": sup, "locationId": env.LocationID,
		"lines": []any{map[string]any{"itemId": flour, "quantity": "75"}}}).Expect(t, 201)
	if po.Str("status") != "approved" || po.Str("lines.0.unitPrice") != "1.2" || !strings.HasPrefix(po.Str("number"), "PO-") {
		t.Fatalf("po: %s", po.Raw)
	}
	// Over the limit needs approval.
	big := env.Do("POST", "/api/v1/purchase-orders", map[string]any{"supplierId": sup, "locationId": env.LocationID,
		"lines": []any{map[string]any{"itemId": flour, "quantity": "100", "unitPrice": "1.50"}}}).Expect(t, 201)
	if big.Str("status") != "awaiting_approval" {
		t.Fatalf("big po: %s", big.Raw)
	}
	env.Do("POST", "/api/v1/purchase-orders/"+big.Str("id")+":send", nil).Expect(t, 409)
	env.Do("POST", "/api/v1/purchase-orders/"+big.Str("id")+":approve", nil).Expect(t, 200)

	// Suggestion now accounts for stock on order.
	if lines := env.Do("GET", "/api/v1/suggested-orders?supplierId="+sup+"&locationId="+env.LocationID, nil).Expect(t, 200).Get("lines").([]any); len(lines) != 0 {
		t.Fatalf("stock on order should cover the par level: %v", lines)
	}

	id := po.Str("id")
	env.Do("POST", "/api/v1/purchase-orders/"+id+":send", nil).Expect(t, 200)
	r := env.Do("POST", "/api/v1/purchase-orders/"+id+":receive", map[string]any{"lines": []any{map[string]any{"itemId": flour, "quantity": "50"}}}).Expect(t, 200)
	if r.Str("status") != "partially_received" || r.Str("lines.0.receivedQty") != "50" {
		t.Fatalf("partial receipt: %s", r.Raw)
	}
	if r = env.Do("POST", "/api/v1/purchase-orders/"+id+":receive", nil).Expect(t, 200); r.Str("status") != "received" {
		t.Fatalf("full receipt: %s", r.Raw)
	}
	if onHand(t, env, flour, env.LocationID) != "75" || level(t, env, flour, env.LocationID).Str("data.0.avgCost") != "1.2" {
		t.Fatal("receipt should add stock at the order price")
	}
	env.Do("POST", "/api/v1/purchase-orders/"+id+":receive", nil).Expect(t, 409)

	// Invoice billing more and at a higher price than received.
	inv := env.Do("POST", "/api/v1/supplier-invoices", map[string]any{"supplierId": sup, "purchaseOrderId": id, "invoiceNumber": "INV-9",
		"invoiceDate": "2026-09-30", "total": "104.00", "lines": []any{map[string]any{"itemId": flour, "quantity": "80", "unitPrice": "1.30"}}}).Expect(t, 201)
	if inv.Str("status") != "mismatch" || len(inv.Get("issues").([]any)) != 2 {
		t.Fatalf("invoice matching: %s", inv.Raw)
	}
	env.Do("POST", "/api/v1/supplier-invoices", map[string]any{"supplierId": sup, "invoiceNumber": "INV-9", "invoiceDate": "2026-09-30", "total": "1"}).Expect(t, 409)
	if env.Do("POST", "/api/v1/supplier-invoices/"+inv.Str("id")+":approve", nil).Expect(t, 200).Str("status") != "approved" {
		t.Fatal("approve invoice")
	}

	env.ProcessWebhooks()
	if got := sortedTypes(env); got != "purchase_order.received,purchase_order.received,purchase_order.sent,supplier_invoice.mismatch" {
		t.Fatalf("events: %s", got)
	}
}

func TestSalesOrdersAndInvoices(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"sales_order.shipped", "invoice.issued", "invoice.paid"})
	mug := env.Do("POST", "/api/v1/items", map[string]any{"sku": "MUG", "name": "Mug", "externalId": "web-mug"}).Expect(t, 201).Str("id")
	env.Do("POST", "/api/v1/inventory/adjustments", map[string]any{"itemId": mug, "locationId": env.LocationID, "quantity": "5", "unitCost": "4", "reason": "opening"}).Expect(t, 201)

	order := map[string]any{"source": "web-store", "locationId": env.LocationID,
		"customer": map[string]any{"name": "Alex Kim", "email": "alex@example.com", "externalId": "cust-1"},
		"lines":    []any{map[string]any{"itemExternalId": "web-mug", "quantity": "3", "unitPrice": "12.00"}}}
	o := env.Do("PUT", "/api/v1/sales-orders/external/web-1", order).Expect(t, 201)
	if o.Str("total") != "36" || o.Str("customerId") == "" {
		t.Fatalf("order: %s", o.Raw)
	}
	if level(t, env, mug, env.LocationID).Str("data.0.reserved") != "3" {
		t.Fatal("order should reserve stock")
	}
	// Update replaces lines and re-reserves.
	order["lines"] = []any{map[string]any{"itemId": mug, "quantity": "4", "unitPrice": "12.00"}}
	o = env.Do("PUT", "/api/v1/sales-orders/external/web-1", order).Expect(t, 200)
	if lv := level(t, env, mug, env.LocationID); lv.Str("data.0.reserved") != "4" || lv.Str("data.0.available") != "1" {
		t.Fatalf("re-reserve: %s", lv.Raw)
	}
	// Same customer is reused by external ID.
	o2 := env.Do("POST", "/api/v1/sales-orders", map[string]any{"locationId": env.LocationID, "customerExternalId": "cust-1",
		"lines": []any{map[string]any{"itemId": mug, "quantity": "2", "unitPrice": "12"}}}).Expect(t, 201)
	if o2.Str("customerId") != o.Str("customerId") {
		t.Fatal("customer should be matched by external ID")
	}
	// Can't ship more than on hand once the first order ships.
	env.Do("POST", "/api/v1/sales-orders/"+o.Str("id")+":ship", map[string]any{"trackingNumber": "1Z999"}).Expect(t, 200)
	r := env.Do("POST", "/api/v1/sales-orders/"+o2.Str("id")+":ship", nil).Expect(t, 422)
	if r.Str("code") != "insufficient_stock" {
		t.Fatalf("expected insufficient_stock: %s", r.Raw)
	}
	env.Do("POST", "/api/v1/sales-orders/"+o2.Str("id")+":cancel", nil).Expect(t, 200)
	if lv := level(t, env, mug, env.LocationID); lv.Str("data.0.onHand") != "1" || lv.Str("data.0.reserved") != "0" {
		t.Fatalf("after ship and cancel: %s", lv.Raw)
	}
	env.Do("PUT", "/api/v1/sales-orders/external/web-1", order).Expect(t, 409) // shipped orders can't change

	inv := env.Do("POST", "/api/v1/sales-orders/"+o.Str("id")+":invoice", nil).Expect(t, 201)
	env.Do("POST", "/api/v1/sales-orders/"+o.Str("id")+":invoice", nil).Expect(t, 409)
	pdf := env.Do("GET", "/api/v1/invoices/"+inv.Str("id")+"/pdf", nil).Expect(t, 200)
	if pdf.Header.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(string(pdf.Raw), "%PDF-1.4") || !strings.Contains(string(pdf.Raw), "Alex Kim") {
		t.Fatalf("pdf: %.80s", pdf.Raw)
	}
	env.Do("POST", "/api/v1/invoices/"+inv.Str("id")+":pay", nil).Expect(t, 200)
	if env.Do("GET", "/api/v1/sales-orders/"+o.Str("id"), nil).Expect(t, 200).Str("paymentStatus") != "paid" {
		t.Fatal("paying the invoice should mark the order paid")
	}
	env.ProcessWebhooks()
	if got := sortedTypes(env); got != "invoice.issued,invoice.paid,sales_order.shipped" {
		t.Fatalf("events: %s", got)
	}
}

func TestCashManagement(t *testing.T) {
	env := testutil.New(t, everyScope, []string{"cash.over_short_exceeded", "cash.deposit_mismatch", "cash.settlement_mismatch", "cash.business_day_closed"})
	day := "2026-09-27"
	env.Do("POST", "/api/v1/cash/tenders", map[string]any{"source": "pos", "tenders": []any{
		map[string]any{"externalId": "d2-cash", "locationExternalId": "101", "businessDate": day, "registerId": "d2", "type": "cash", "amount": "412.50"},
		map[string]any{"externalId": "d2-card", "locationExternalId": "101", "businessDate": day, "registerId": "d2", "type": "card", "amount": "980.00"},
	}}).Expect(t, 202)

	count := func(kind, counted string) testutil.Response {
		return env.Do("POST", "/api/v1/cash/counts", map[string]any{"locationId": env.LocationID, "businessDate": day, "registerId": "d2", "kind": kind, "counted": counted})
	}
	count("open", "150").Expect(t, 201)
	count("skim", "300").Expect(t, 201)
	// Expected = 150 + 412.50 − 300 = 262.50; counted 250 → −12.50 (beyond the default $5 tolerance).
	c := count("close", "250").Expect(t, 201)
	if c.Str("expected") != "262.5" || c.Str("overShort") != "-12.5" {
		t.Fatalf("close count: %s", c.Raw)
	}

	// Card settlement short by $20.
	env.Do("POST", "/api/v1/cash/settlements", map[string]any{"source": "stripe", "settlements": []any{
		map[string]any{"externalId": "po_1", "locationId": env.LocationID, "provider": "Processor", "tenderType": "card",
			"businessDate": day, "gross": "960.00", "fees": "28.00", "currency": "USD"}}}).Expect(t, 202)

	dep := env.Do("POST", "/api/v1/cash/deposits", map[string]any{"locationId": env.LocationID, "businessDate": day, "bagNumber": "B-7781", "amount": "550.00"}).Expect(t, 201)
	env.Do("POST", "/api/v1/cash/bank-transactions", map[string]any{"source": "bank", "transactions": []any{
		map[string]any{"externalId": "bt-1", "bookedOn": "2026-09-28", "amount": "549.50", "reference": "DEPOSIT B-7781", "currency": "USD"}}}).Expect(t, 202)
	deps := env.Do("GET", "/api/v1/cash/deposits", nil).Expect(t, 200)
	if deps.Str("data.0.id") != dep.Str("id") || deps.Str("data.0.status") != "mismatch" || deps.Str("data.0.difference") != "-0.5" {
		t.Fatalf("deposit matching: %s", deps.Raw)
	}

	days := env.Do("GET", "/api/v1/cash/business-days?locationId="+env.LocationID+"&from="+day+"&to="+day, nil).Expect(t, 200)
	if days.Str("data.0.cashExpected") != "412.5" || days.Str("data.0.unsettled") != "20" || days.Str("data.0.overShort") != "-12.5" {
		t.Fatalf("business day: %s", days.Raw)
	}
	env.Do("POST", "/api/v1/cash/business-days/"+env.LocationID+"/"+day+":close", nil).Expect(t, 200)
	env.Do("POST", "/api/v1/cash/business-days/"+env.LocationID+"/"+day+":close", nil).Expect(t, 409)
	count("close", "1").Expect(t, 409)

	// Late POS data for a closed day is accepted but flagged.
	r := env.Do("POST", "/api/v1/sales/transactions:batch", map[string]any{"source": "pos", "transactions": []any{map[string]any{
		"externalId": "late", "locationExternalId": "101", "occurredAt": "2026-09-27T18:00:00Z", "total": "5"}}}).Expect(t, 202)
	if !strings.Contains(r.Str("results.0.errors.0.message"), "closed") {
		t.Fatalf("expected closed-day warning: %s", r.Raw)
	}

	env.ProcessWebhooks()
	got := strings.Join(env.Hooks.Types(), ",")
	for _, want := range []string{"cash.over_short_exceeded", "cash.settlement_mismatch", "cash.deposit_mismatch", "cash.business_day_closed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}
