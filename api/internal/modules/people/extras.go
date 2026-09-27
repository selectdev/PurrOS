package people

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/selectdev/purros/api/internal/crud"
	"github.com/selectdev/purros/api/internal/db"
	"github.com/selectdev/purros/api/internal/httpx"
	"github.com/selectdev/purros/api/internal/ids"
	"github.com/selectdev/purros/api/internal/ingest"
	"github.com/selectdev/purros/api/internal/refs"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Transfer and rehire
// ---------------------------------------------------------------------------

type TransferInput struct {
	EffectiveDate  *httpx.Date `json:"effectiveDate,omitempty" doc:"Defaults to today"`
	HomeLocationID *string     `json:"homeLocationId,omitempty"`
	DepartmentID   *string     `json:"departmentId,omitempty"`
	Position       *string     `json:"position,omitempty" validate:"omitempty,max=100"`
	ManagerID      *string     `json:"managerId,omitempty"`
}

type RehireInput struct {
	StartDate httpx.Date `json:"startDate" validate:"required"`
}

// ---------------------------------------------------------------------------
// Documents
// ---------------------------------------------------------------------------

type Document struct {
	ID                 string      `json:"id" db:"id"`
	EmployeeID         string      `json:"employeeId" db:"employee_id"`
	Type               string      `json:"type" db:"type"`
	Name               string      `json:"name" db:"name"`
	URL                *string     `json:"url" db:"url"`
	ExpiresOn          *httpx.Date `json:"expiresOn" db:"expires_on"`
	SharedWithEmployee bool        `json:"sharedWithEmployee" db:"shared_with_employee"`
	CreatedAt          time.Time   `json:"createdAt" db:"created_at"`
	UpdatedAt          time.Time   `json:"updatedAt" db:"updated_at"`
	ArchivedAt         *time.Time  `json:"archivedAt" db:"archived_at"`
}

type DocumentInput struct {
	EmployeeID         string      `json:"-" db:"employee_id"`
	Type               string      `json:"type" db:"type" validate:"required,max=50" doc:"e.g. contract, id, certificate"`
	Name               string      `json:"name" db:"name" validate:"required,max=200"`
	URL                *string     `json:"url,omitempty" db:"url" validate:"omitempty,url"`
	ExpiresOn          *httpx.Date `json:"expiresOn,omitempty" db:"expires_on"`
	SharedWithEmployee bool        `json:"sharedWithEmployee,omitempty" db:"shared_with_employee"`
}

var documents = &crud.Resource[Document, DocumentInput]{
	Path: "/employees/{employeeId}/documents", ParentParam: "employeeId", ParentColumn: "employee_id",
	Table: "employee_documents", Prefix: ids.Document, Noun: "employee_document", Tag: "People",
	Feature: "people.documents", ReadScope: "people:read", WriteScope: "people:write", Archive: true,
	Check: func(c *httpx.Ctx, q db.Querier, in *DocumentInput, before *Document) error {
		if before == nil {
			_, err := get(c, q, "id = $1", in.EmployeeID, false)
			return err
		}
		return nil
	},
}

// ---------------------------------------------------------------------------
// Skills
// ---------------------------------------------------------------------------

type Skill struct {
	ID         string     `json:"id" db:"id"`
	Name       string     `json:"name" db:"name"`
	Category   *string    `json:"category" db:"category"`
	ValidDays  *int       `json:"validDays" db:"valid_days"`
	ExternalID *string    `json:"externalId" db:"external_id"`
	CreatedAt  time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time  `json:"updatedAt" db:"updated_at"`
	ArchivedAt *time.Time `json:"archivedAt" db:"archived_at"`
}

type SkillInput struct {
	Name       string  `json:"name" db:"name" validate:"required,max=100"`
	Category   *string `json:"category,omitempty" db:"category" validate:"omitempty,max=50"`
	ValidDays  *int    `json:"validDays,omitempty" db:"valid_days" validate:"omitempty,min=1" doc:"Certifications expire this many days after they are obtained"`
	ExternalID *string `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
}

var skills = &crud.Resource[Skill, SkillInput]{
	Path: "/skills", Table: "skills", Prefix: ids.Skill, Noun: "skill", Tag: "People",
	Feature: "people.skills", ReadScope: "people:read", WriteScope: "people:write", External: true, Archive: true,
}

type EmployeeSkill struct {
	SkillID    string      `json:"skillId"`
	Name       string      `json:"name"`
	ObtainedOn *httpx.Date `json:"obtainedOn"`
	ExpiresOn  *httpx.Date `json:"expiresOn"`
	Expired    bool        `json:"expired"`
}

type EmployeeSkillInput struct {
	ObtainedOn *httpx.Date `json:"obtainedOn,omitempty"`
	ExpiresOn  *httpx.Date `json:"expiresOn,omitempty" doc:"Defaults to obtainedOn + the skill's validDays"`
}

type skillList struct {
	Data []EmployeeSkill `json:"data"`
}

func listEmployeeSkills(c *httpx.Ctx, q db.Querier, empID string) (skillList, error) {
	rows, err := q.Query(c, `
		SELECT s.id, s.name, es.obtained_on, es.expires_on
		FROM employee_skills es JOIN skills s ON s.id = es.skill_id
		WHERE es.employee_id = $1 ORDER BY s.name`, empID)
	if err != nil {
		return skillList{}, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (EmployeeSkill, error) {
		var s EmployeeSkill
		var ob, ex *time.Time
		if err := r.Scan(&s.SkillID, &s.Name, &ob, &ex); err != nil {
			return s, err
		}
		if ob != nil {
			d := httpx.NewDate(*ob)
			s.ObtainedOn = &d
		}
		if ex != nil {
			d := httpx.NewDate(*ex)
			s.ExpiresOn = &d
			s.Expired = ex.Before(time.Now())
		}
		return s, nil
	})
	if out == nil {
		out = []EmployeeSkill{}
	}
	return skillList{Data: out}, err
}

// ---------------------------------------------------------------------------
// Pay rates
// ---------------------------------------------------------------------------

type PayRate struct {
	ID            string          `json:"id"`
	EmployeeID    string          `json:"employeeId"`
	PayType       string          `json:"payType"`
	Rate          decimal.Decimal `json:"rate"`
	Currency      string          `json:"currency"`
	EffectiveFrom httpx.Date      `json:"effectiveFrom"`
	CreatedAt     time.Time       `json:"createdAt"`
}

type PayRateInput struct {
	PayType       string           `json:"payType" validate:"required,oneof=hourly salary" doc:"salary rates are per year"`
	Rate          *decimal.Decimal `json:"rate" validate:"required"`
	Currency      string           `json:"currency,omitempty" validate:"omitempty,currency"`
	EffectiveFrom httpx.Date       `json:"effectiveFrom" validate:"required"`
}

type payRateList struct {
	Data []PayRate `json:"data"`
}

// CurrentRate returns the hourly-equivalent rate in effect on a date, or nil.
func CurrentRate(c *httpx.Ctx, q db.Querier, empID string, on time.Time) (*decimal.Decimal, string, error) {
	var payType, currency string
	var rate decimal.Decimal
	err := q.QueryRow(c, `SELECT pay_type, rate, currency FROM pay_rates
		WHERE employee_id = $1 AND effective_from <= $2 ORDER BY effective_from DESC LIMIT 1`, empID, on).
		Scan(&payType, &rate, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if payType == "salary" {
		rate = rate.Div(decimal.NewFromInt(2080)) // 52 weeks × 40 h
	}
	return &rate, currency, nil
}

// ---------------------------------------------------------------------------
// Payslips
// ---------------------------------------------------------------------------

type Payslip struct {
	ID          string           `json:"id"`
	Source      string           `json:"source"`
	ExternalID  string           `json:"externalId"`
	EmployeeID  string           `json:"employeeId"`
	PeriodStart httpx.Date       `json:"periodStart"`
	PeriodEnd   httpx.Date       `json:"periodEnd"`
	PayDate     *httpx.Date      `json:"payDate"`
	Gross       decimal.Decimal  `json:"gross"`
	Net         *decimal.Decimal `json:"net"`
	Currency    string           `json:"currency"`
	URL         *string          `json:"url"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}

type PayslipInput struct {
	ExternalID string `json:"externalId" validate:"required,extid"`
	refs.EmployeeRef
	PeriodStart httpx.Date       `json:"periodStart" validate:"required"`
	PeriodEnd   httpx.Date       `json:"periodEnd" validate:"required"`
	PayDate     *httpx.Date      `json:"payDate,omitempty"`
	Gross       *decimal.Decimal `json:"gross" validate:"required"`
	Net         *decimal.Decimal `json:"net,omitempty"`
	Currency    string           `json:"currency" validate:"required,currency"`
	URL         string           `json:"url,omitempty" validate:"omitempty,url" doc:"Link to the payslip PDF"`
}

type PayslipBatch struct {
	Source   string         `json:"source" validate:"required"`
	Payslips []PayslipInput `json:"payslips" validate:"required"`
}

const payslipCols = `id, source, external_id, employee_id, period_start, period_end, pay_date, gross, net, currency, url, created_at, updated_at`

func scanPayslip(r pgx.Row) (Payslip, error) {
	var p Payslip
	var ps, pe time.Time
	var pd *time.Time
	err := r.Scan(&p.ID, &p.Source, &p.ExternalID, &p.EmployeeID, &ps, &pe, &pd, &p.Gross, &p.Net, &p.Currency, &p.URL, &p.CreatedAt, &p.UpdatedAt)
	p.PeriodStart, p.PeriodEnd = httpx.NewDate(ps), httpx.NewDate(pe)
	if pd != nil {
		d := httpx.NewDate(*pd)
		p.PayDate = &d
	}
	return p, err
}

// ExtraRoutes are the People routes beyond the core employee record.
func ExtraRoutes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "POST", Path: "/employees/{id}:transfer", Tag: "People", Feature: feature, Scope: "people:write",
			Summary: "Transfer an employee (location, department, position or manager)", Body: TransferInput{}, Response: Employee{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in TransferInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Employee
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := get(c, tx, "id = $1", c.Param("id"), true)
					if err != nil {
						return err
					}
					if before.Status == "terminated" {
						return httpx.Conflict("Terminated employees can't be transferred. Rehire them first.")
					}
					next := before.toInput()
					if in.HomeLocationID != nil {
						next.HomeLocationID = in.HomeLocationID
					}
					if in.DepartmentID != nil {
						next.DepartmentID = in.DepartmentID
					}
					if in.Position != nil {
						next.Position = in.Position
					}
					if in.ManagerID != nil {
						next.ManagerID = in.ManagerID
					}
					out, err = update(c, tx, before, next)
					if err != nil {
						return err
					}
					eff := httpx.NewDate(time.Now())
					if in.EffectiveDate != nil {
						eff = *in.EffectiveDate
					}
					return c.Record(tx, httpx.Change{
						Action: "employee.transfer", EventType: "employee.transferred", Feature: feature,
						EntityType: "employee", EntityID: out.ID, LocationID: deref(out.HomeLocationID),
						Before: before, After: map[string]any{"employee": out, "effectiveDate": eff},
					})
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/employees/{id}:rehire", Tag: "People", Feature: feature, Scope: "people:write",
			Summary: "Rehire a terminated employee", Body: RehireInput{}, Response: Employee{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in RehireInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Employee
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := get(c, tx, "id = $1", c.Param("id"), true)
					if err != nil {
						return err
					}
					if before.Status != "terminated" {
						return httpx.Conflict("Only terminated employees can be rehired.")
					}
					out, err = scan(tx.QueryRow(c, `UPDATE employees SET status='active', start_date=$2, end_date=NULL,
						termination_reason=NULL, archived_at=NULL, version=version+1, updated_at=now()
						WHERE id=$1 RETURNING `+cols, before.ID, in.StartDate))
					if err != nil {
						return err
					}
					return c.Record(tx, httpx.Change{Action: "employee.rehire", EventType: "employee.updated", Feature: feature,
						EntityType: "employee", EntityID: out.ID, LocationID: deref(out.HomeLocationID), Before: before, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/employees/{id}/skills", Tag: "People", Feature: "people.skills", Scope: "people:read",
			Summary: "An employee's skills and certifications", Response: skillList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				if _, err := get(c, c.App.Pool, "id = $1", c.Param("id"), false); err != nil {
					return nil, err
				}
				return listEmployeeSkills(c, c.App.Pool, c.Param("id"))
			},
		},
		{
			Method: "PUT", Path: "/employees/{id}/skills/{skillId}", Tag: "People", Feature: "people.skills", Scope: "people:write",
			Summary: "Give an employee a skill or certification", Body: EmployeeSkillInput{}, Response: skillList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in EmployeeSkillInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out skillList
				err := c.InTx(func(tx pgx.Tx) error {
					if _, err := get(c, tx, "id = $1", c.Param("id"), false); err != nil {
						return err
					}
					sk, err := skills.Get(c, tx, "id", c.Param("skillId"), false)
					if err != nil {
						return err
					}
					expires := in.ExpiresOn
					if expires == nil && in.ObtainedOn != nil && sk.ValidDays != nil {
						d := httpx.NewDate(in.ObtainedOn.AddDate(0, 0, *sk.ValidDays))
						expires = &d
					}
					if _, err := tx.Exec(c, `
						INSERT INTO employee_skills (employee_id, skill_id, obtained_on, expires_on) VALUES ($1, $2, $3, $4)
						ON CONFLICT (employee_id, skill_id) DO UPDATE SET obtained_on = EXCLUDED.obtained_on, expires_on = EXCLUDED.expires_on`,
						c.Param("id"), sk.ID, in.ObtainedOn, expires); err != nil {
						return err
					}
					if err := c.Record(tx, httpx.Change{Action: "employee.skill_set", EntityType: "employee", EntityID: c.Param("id"),
						After: map[string]any{"skillId": sk.ID, "expiresOn": expires}}); err != nil {
						return err
					}
					out, err = listEmployeeSkills(c, tx, c.Param("id"))
					return err
				})
				return out, err
			},
		},
		{
			Method: "DELETE", Path: "/employees/{id}/skills/{skillId}", Tag: "People", Feature: "people.skills", Scope: "people:write",
			Summary: "Remove a skill from an employee", Response: skillList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var out skillList
				err := c.InTx(func(tx pgx.Tx) error {
					if _, err := tx.Exec(c, `DELETE FROM employee_skills WHERE employee_id=$1 AND skill_id=$2`, c.Param("id"), c.Param("skillId")); err != nil {
						return err
					}
					if err := c.Record(tx, httpx.Change{Action: "employee.skill_remove", EntityType: "employee", EntityID: c.Param("id"),
						After: map[string]any{"skillId": c.Param("skillId")}}); err != nil {
						return err
					}
					var err error
					out, err = listEmployeeSkills(c, tx, c.Param("id"))
					return err
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/employees/{id}/pay-rates", Tag: "People", Feature: feature, Scope: "payroll:read",
			Summary: "Pay rate history (newest first)", Response: payRateList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT id, employee_id, pay_type, rate, currency, effective_from, created_at
					FROM pay_rates WHERE employee_id = $1 ORDER BY effective_from DESC`, c.Param("id"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PayRate, error) {
					var p PayRate
					var eff time.Time
					err := r.Scan(&p.ID, &p.EmployeeID, &p.PayType, &p.Rate, &p.Currency, &eff, &p.CreatedAt)
					p.EffectiveFrom = httpx.NewDate(eff)
					return p, err
				})
				if list == nil {
					list = []PayRate{}
				}
				return payRateList{Data: list}, err
			},
		},
		{
			Method: "POST", Path: "/employees/{id}/pay-rates", Tag: "People", Feature: feature, Scope: "payroll:write",
			Summary:     "Add a pay rate",
			Description: "Rates are effective-dated: past timesheets keep the rate that applied at the time. Posting the same effectiveFrom replaces that rate.",
			Body:        PayRateInput{}, Response: PayRate{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in PayRateInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				if in.Rate.IsNegative() {
					return nil, httpx.Validation(httpx.FieldError{Path: "rate", Message: "Must be zero or more"})
				}
				var out PayRate
				err := c.InTx(func(tx pgx.Tx) error {
					if _, err := get(c, tx, "id = $1", c.Param("id"), false); err != nil {
						return err
					}
					if in.Currency == "" {
						if err := tx.QueryRow(c, `SELECT currency FROM company LIMIT 1`).Scan(&in.Currency); err != nil {
							return err
						}
					}
					var eff time.Time
					err := tx.QueryRow(c, `
						INSERT INTO pay_rates (id, employee_id, pay_type, rate, currency, effective_from) VALUES ($1,$2,$3,$4,$5,$6)
						ON CONFLICT (employee_id, effective_from) DO UPDATE SET pay_type=EXCLUDED.pay_type, rate=EXCLUDED.rate, currency=EXCLUDED.currency
						RETURNING id, employee_id, pay_type, rate, currency, effective_from, created_at`,
						ids.New(ids.PayRate), c.Param("id"), in.PayType, *in.Rate, in.Currency, in.EffectiveFrom).
						Scan(&out.ID, &out.EmployeeID, &out.PayType, &out.Rate, &out.Currency, &eff, &out.CreatedAt)
					if err != nil {
						return err
					}
					out.EffectiveFrom = httpx.NewDate(eff)
					return c.Record(tx, httpx.Change{Action: "pay_rate.set", EventType: "pay_rate.changed", Feature: feature,
						EntityType: "employee", EntityID: out.EmployeeID, After: out})
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/payslips", Tag: "People", Feature: "employee_area.payslips", Scope: "payroll:write",
			Ingest: true, Status: 202, Summary: "Send payslips from your payroll provider",
			Description: "Payslips appear in each employee's Employee Area. Unique on (source, externalId).",
			Body:        PayslipBatch{}, Response: httpx.BatchResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return ingest.Run(c, "payslips", "payslips", func(e *ingest.Env, i int, in PayslipInput) (ingest.Outcome, error) {
					if in.PeriodEnd.Before(in.PeriodStart.Time) {
						return ingest.Outcome{}, ingest.Rejectf("periodEnd", "Must not be before periodStart")
					}
					emp, err := refs.Employee(c, e.Tx, in.EmployeeRef, "", false)
					if err != nil {
						return ingest.Outcome{}, err
					}
					var id string
					var inserted bool
					err = e.Tx.QueryRow(c, `
						INSERT INTO payslips (id, source, external_id, employee_id, period_start, period_end, pay_date, gross, net, currency, url)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,nullif($11,''))
						ON CONFLICT (source, external_id) DO UPDATE SET employee_id=EXCLUDED.employee_id, period_start=EXCLUDED.period_start,
							period_end=EXCLUDED.period_end, pay_date=EXCLUDED.pay_date, gross=EXCLUDED.gross, net=EXCLUDED.net,
							currency=EXCLUDED.currency, url=EXCLUDED.url, updated_at=now()
						RETURNING id, (xmax = 0)`,
						ids.New(ids.Payslip), e.Source, in.ExternalID, emp, in.PeriodStart, in.PeriodEnd, in.PayDate,
						*in.Gross, in.Net, in.Currency, in.URL).Scan(&id, &inserted)
					return ingest.Outcome{Status: ingest.Status(inserted), ID: id}, err
				})
			},
		},
		{
			Method: "GET", Path: "/payslips", Tag: "People", Feature: "employee_area.payslips", Scope: "payroll:read",
			Summary: "List payslips", Response: httpx.Page[Payslip]{},
			Handler: func(c *httpx.Ctx) (any, error) {
				lp, err := c.ParseList()
				if err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `SELECT `+payslipCols+` FROM payslips
					WHERE id > $1 AND ($2 = '' OR employee_id = $2) ORDER BY id LIMIT $3`,
					lp.AfterID, c.Query("employeeId"), lp.Limit+1)
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payslip, error) { return scanPayslip(r) })
				if err != nil {
					return nil, err
				}
				return httpx.NewPage(list, lp.Limit, func(p Payslip) string { return p.ID }), nil
			},
		},
	}
	routes = append(routes, documents.Routes()...)
	routes = append(routes, skills.Routes()...)
	return routes
}
