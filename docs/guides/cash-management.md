# Cash management

Feature key: `cash`. Needs Sales, so PurrOS knows how much money to expect.

> **Status:** The API records tenders, card settlements, bank transactions, drawer counts with over/short, deposits with matching, and business-day close. Paid-outs and petty cash, blind counts, two-person verification and the guided screens are planned. Screens described below arrive with the web app; until then, use the endpoints listed at the end of this page.

Follow every cash and card payment from the register to the bank, spot shortages the same day, and keep a full audit trail.

## The register-to-bank workflow

```
POS sales & tenders ──► Expected per drawer
                               │
Drawer counts (open / shift change / close) ──► Over/short
        │
Skims & safe drops ──► Safe ──► Safe count
                                  │
                         Bank deposit ──► Verified against bank data
```

Each location's **cash home page** lists what's due now, such as "Close drawer 2", "Safe count overdue" or "Deposit not yet recorded", with quick links.

## Drawers and counts

- Set up **drawers/tills** per location with a standard starting float.
- **Opening count:** confirm the float.
- **Skim / safe drop:** move cash from a drawer to the safe during the day, e.g. whenever a drawer goes over a limit.
- **Shift-change and closing counts:** count by denomination on a phone or tablet. PurrOS compares the count with the expected cash from the POS and shows **over/short**.
- **Blind counts** (optional): the counter doesn't see the expected amount until the count is saved.
- **Two-person verification** (optional) above a set amount.

Counts need `cash.count`, and closing the business day needs `cash.manage`.

## Safe and change

- **Safe counts** at set times, with expected balance from all drops, deposits and change orders.
- **Change orders:** record coins and notes bought from the bank.

## Bank deposits

1. Prepare a deposit from the safe (`cash.deposits`) and record the **bag number**, amount and who took it.
2. With **deposit verification** (`cash.deposit_verification`), PurrOS matches deposits against bank data sent by an integration or imported from a bank statement file.
3. Deposits that are late, missing or different from the bank's amount are flagged and alert the right people.

## Card and digital payments

With **tender reconciliation** (`cash.tender_reconciliation`), PurrOS compares what the POS says was paid by card, digital wallet, gift card or third-party platform with what the **processor or platform actually settled**. Differences are flagged for someone with `cash.reconcile` to investigate.

## Paid-outs and petty cash

With `cash.petty_cash`, staff can record cash paid out of a drawer or petty cash for small purchases, with a **receipt photo** and category. Amounts above the location's limit need approval (`cash.paid_outs.approve`).

## Thresholds and alerts

Set per location, or inherit from above:

- over/short tolerance per drawer and per day
- maximum cash in a drawer before a skim is due
- deposit deadline (e.g. next business day by 12:00)

Crossing a threshold alerts the manager, and can alert a district manager too.

## Reports

Over/short by drawer, shift, employee and location, deposit status, reconciliation differences, paid-outs by category, and trends across locations.

## API & events

| Endpoint | Scope |
|---|---|
| `POST /api/v1/cash/tenders` (expected tender totals per drawer or shift) | `cash:write` |
| `POST /api/v1/cash/settlements` (processor and platform payouts) | `cash:write` |
| `POST /api/v1/cash/bank-transactions` (bank data for deposit verification) | `cash:write` |
| `GET /api/v1/cash/counts`, `/deposits`, `/business-days` | `cash:read` |

Events: `cash.count_completed`, `cash.over_short_exceeded`, `cash.deposit_recorded`, `cash.deposit_mismatch`, `cash.settlement_mismatch`, `cash.business_day_closed`.

See [Data ingestion](../api/data-ingestion.md) for how POS data arrives.
