// Package operations serves forms and checklists, submissions with automatic
// failed-answer detection, corrective actions, audits and sensors.
package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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

const (
	feature = "operations"
	tag     = "Operations"
)

// ---------------------------------------------------------------------------
// Forms
// ---------------------------------------------------------------------------

// Question is one question of a form.
type Question struct {
	ID           string           `json:"id" validate:"required,max=50"`
	Text         string           `json:"text" validate:"required,max=500"`
	Type         string           `json:"type" validate:"required,oneof=yes_no number temperature choice text photo signature date" `
	Required     bool             `json:"required,omitempty"`
	Min          *decimal.Decimal `json:"min,omitempty" doc:"number/temperature: lowest passing value"`
	Max          *decimal.Decimal `json:"max,omitempty" doc:"number/temperature: highest passing value"`
	Options      []string         `json:"options,omitempty" doc:"choice: allowed answers"`
	FailOptions  []string         `json:"failOptions,omitempty" doc:"choice: answers that fail"`
	Expected     *bool            `json:"expected,omitempty" doc:"yes_no: passing answer (default true)"`
	Critical     bool             `json:"critical,omitempty" doc:"A failure fails the whole submission"`
	Weight       *decimal.Decimal `json:"weight,omitempty" doc:"Audit scoring weight (default 1)"`
	CreateAction bool             `json:"createAction,omitempty" doc:"Create a corrective action when this fails"`
}

type Form struct {
	ID         string     `json:"id" db:"id"`
	Name       string     `json:"name" db:"name"`
	Category   *string    `json:"category" db:"category"`
	Kind       string     `json:"kind" db:"kind" doc:"checklist, audit, log or report"`
	Questions  []Question `json:"questions" db:"questions"`
	ExternalID *string    `json:"externalId" db:"external_id"`
	Version    int        `json:"version" db:"version"`
	CreatedAt  time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time  `json:"updatedAt" db:"updated_at"`
	ArchivedAt *time.Time `json:"archivedAt" db:"archived_at"`
}

type FormInput struct {
	Name       string     `json:"name" db:"name" validate:"required,max=200"`
	Category   *string    `json:"category,omitempty" db:"category" validate:"omitempty,max=100"`
	Kind       string     `json:"kind,omitempty" db:"kind" validate:"omitempty,oneof=checklist audit log report"`
	Questions  []Question `json:"questions" db:"questions" validate:"required,min=1,dive"`
	ExternalID *string    `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
}

var forms = &crud.Resource[Form, FormInput]{
	Path: "/forms", Table: "forms", Prefix: ids.Form, Noun: "form", Tag: tag,
	Feature: feature, ReadScope: "operations:read", WriteScope: "operations:write", External: true, Archive: true,
	Filters: []crud.Filter{{Query: "kind", Column: "kind"}, {Query: "category", Column: "category"}},
	Defaults: func(in *FormInput) {
		if in.Kind == "" {
			in.Kind = "checklist"
		}
	},
	Check: func(c *httpx.Ctx, q db.Querier, in *FormInput, before *Form) error {
		if in.Kind == "" {
			in.Kind = "checklist"
		}
		seen := map[string]bool{}
		for i, qn := range in.Questions {
			path := fmt.Sprintf("questions[%d]", i)
			if seen[qn.ID] {
				return httpx.Validation(httpx.FieldError{Path: path + ".id", Message: "Duplicate question id"})
			}
			seen[qn.ID] = true
			if qn.Type == "choice" && len(qn.Options) == 0 {
				return httpx.Validation(httpx.FieldError{Path: path + ".options", Message: "Required for choice questions"})
			}
			if qn.Min != nil && qn.Max != nil && qn.Min.GreaterThan(*qn.Max) {
				return httpx.Validation(httpx.FieldError{Path: path + ".max", Message: "Must be at least min"})
			}
		}
		return nil
	},
}

// ---------------------------------------------------------------------------
// Submissions
// ---------------------------------------------------------------------------

type Result struct {
	QuestionID string `json:"questionId"`
	Answer     any    `json:"answer"`
	Passed     *bool  `json:"passed" doc:"null for questions that can't fail (text, photo…)"`
	Message    string `json:"message,omitempty"`
}

type Submission struct {
	ID          string           `json:"id"`
	FormID      string           `json:"formId"`
	FormVersion int              `json:"formVersion"`
	LocationID  *string          `json:"locationId"`
	EmployeeID  *string          `json:"employeeId"`
	SubmittedAt time.Time        `json:"submittedAt"`
	Answers     map[string]any   `json:"answers"`
	Results     []Result         `json:"results"`
	FailedCount int              `json:"failedCount"`
	Score       *decimal.Decimal `json:"score" doc:"Audits: percentage of weighted points earned"`
	MaxScore    *decimal.Decimal `json:"maxScore"`
	Passed      *bool            `json:"passed"`
	Source      *string          `json:"source"`
	ExternalID  *string          `json:"externalId"`
	CreatedAt   time.Time        `json:"createdAt"`
}

type SubmissionInput struct {
	FormID string `json:"formId" validate:"required"`
	refs.LocationRef
	refs.EmployeeRef
	SubmittedAt *time.Time     `json:"submittedAt,omitempty"`
	Answers     map[string]any `json:"answers" validate:"required" doc:"Question id → answer"`
	Source      string         `json:"source,omitempty" validate:"max=100"`
	ExternalID  string         `json:"externalId,omitempty" validate:"omitempty,extid" doc:"With source, re-sending returns the existing submission"`
}

const auditPassMark = 80

func toDecimal(v any) (decimal.Decimal, bool) {
	switch x := v.(type) {
	case float64:
		return decimal.NewFromFloat(x), true
	case string:
		d, err := decimal.NewFromString(x)
		return d, err == nil
	case json.Number:
		d, err := decimal.NewFromString(string(x))
		return d, err == nil
	}
	return decimal.Zero, false
}

// evaluate checks answers against the questions.
func evaluate(form Form, answers map[string]any) ([]Result, []httpx.FieldError, int, *decimal.Decimal, *decimal.Decimal, bool) {
	var results []Result
	var errs []httpx.FieldError
	failed := 0
	criticalFailed := false
	earned, possible := decimal.Zero, decimal.Zero
	yes, no := true, false
	for _, q := range form.Questions {
		ans, present := answers[q.ID]
		if !present || ans == nil || ans == "" {
			if q.Required {
				errs = append(errs, httpx.FieldError{Path: "answers." + q.ID, Message: "Required"})
			}
			continue
		}
		r := Result{QuestionID: q.ID, Answer: ans}
		var pass *bool
		switch q.Type {
		case "yes_no":
			b, ok := ans.(bool)
			if !ok {
				errs = append(errs, httpx.FieldError{Path: "answers." + q.ID, Message: "Must be true or false"})
				continue
			}
			want := true
			if q.Expected != nil {
				want = *q.Expected
			}
			if b == want {
				pass = &yes
			} else {
				pass, r.Message = &no, "Unexpected answer"
			}
		case "number", "temperature":
			d, ok := toDecimal(ans)
			if !ok {
				errs = append(errs, httpx.FieldError{Path: "answers." + q.ID, Message: "Must be a number"})
				continue
			}
			pass = &yes
			if q.Min != nil && d.LessThan(*q.Min) {
				pass, r.Message = &no, fmt.Sprintf("Below the minimum of %s", q.Min)
			}
			if q.Max != nil && d.GreaterThan(*q.Max) {
				pass, r.Message = &no, fmt.Sprintf("Above the maximum of %s", q.Max)
			}
		case "choice":
			s, ok := ans.(string)
			if !ok || !slices.Contains(q.Options, s) {
				errs = append(errs, httpx.FieldError{Path: "answers." + q.ID, Message: "Must be one of: " + strings.Join(q.Options, ", ")})
				continue
			}
			if len(q.FailOptions) > 0 {
				pass = &yes
				if slices.Contains(q.FailOptions, s) {
					pass, r.Message = &no, "Failing option"
				}
			}
		}
		r.Passed = pass
		if pass != nil {
			w := decimal.NewFromInt(1)
			if q.Weight != nil {
				w = *q.Weight
			}
			possible = possible.Add(w)
			if *pass {
				earned = earned.Add(w)
			} else {
				failed++
				criticalFailed = criticalFailed || q.Critical
			}
		}
		results = append(results, r)
	}
	var score, max *decimal.Decimal
	passed := !criticalFailed
	if form.Kind == "audit" && possible.IsPositive() {
		s := earned.Div(possible).Mul(decimal.NewFromInt(100)).Round(2)
		m := decimal.NewFromInt(100)
		score, max = &s, &m
		passed = passed && s.GreaterThanOrEqual(decimal.NewFromInt(auditPassMark))
	}
	if results == nil {
		results = []Result{}
	}
	return results, errs, failed, score, max, passed
}

const subCols = `id, form_id, form_version, location_id, employee_id, submitted_at, answers, results, failed_count, score, max_score,
	passed, source, external_id, created_at`

func scanSub(r pgx.Row) (Submission, error) {
	var s Submission
	err := r.Scan(&s.ID, &s.FormID, &s.FormVersion, &s.LocationID, &s.EmployeeID, &s.SubmittedAt, &s.Answers, &s.Results,
		&s.FailedCount, &s.Score, &s.MaxScore, &s.Passed, &s.Source, &s.ExternalID, &s.CreatedAt)
	return s, err
}

func getSub(ctx context.Context, q db.Querier, id string) (Submission, error) {
	s, err := scanSub(q.QueryRow(ctx, `SELECT `+subCols+` FROM form_submissions WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return s, httpx.NotFound("Submission not found.")
	}
	return s, err
}

// ---------------------------------------------------------------------------
// Corrective actions
// ---------------------------------------------------------------------------

type CorrectiveAction struct {
	ID           string      `json:"id" db:"id"`
	SubmissionID *string     `json:"submissionId" db:"submission_id"`
	QuestionID   *string     `json:"questionId" db:"question_id"`
	LocationID   *string     `json:"locationId" db:"location_id"`
	Title        string      `json:"title" db:"title"`
	AssigneeID   *string     `json:"assigneeId" db:"assignee_id"`
	DueOn        *httpx.Date `json:"dueOn" db:"due_on"`
	Status       string      `json:"status" db:"status"`
	Resolution   *string     `json:"resolution" db:"resolution"`
	EvidenceURL  *string     `json:"evidenceUrl" db:"evidence_url"`
	ClosedAt     *time.Time  `json:"closedAt" db:"closed_at"`
	Version      int         `json:"version" db:"version"`
	CreatedAt    time.Time   `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time   `json:"updatedAt" db:"updated_at"`
}

type CorrectiveActionInput struct {
	LocationID *string     `json:"locationId,omitempty" db:"location_id"`
	Title      string      `json:"title" db:"title" validate:"required,max=300"`
	AssigneeID *string     `json:"assigneeId,omitempty" db:"assignee_id"`
	DueOn      *httpx.Date `json:"dueOn,omitempty" db:"due_on"`
}

var actions = &crud.Resource[CorrectiveAction, CorrectiveActionInput]{
	Path: "/corrective-actions", Table: "corrective_actions", Prefix: ids.CorrectiveAct, Noun: "corrective_action", Tag: tag,
	Feature: "operations.corrective_actions", ReadScope: "operations:read", WriteScope: "operations:write",
	CreatedEvent: "corrective_action.created",
	Filters:      []crud.Filter{{Query: "status", Column: "status"}, {Query: "locationId", Column: "location_id"}, {Query: "assigneeId", Column: "assignee_id"}},
	LocationOf: func(a *CorrectiveAction) string {
		if a.LocationID == nil {
			return ""
		}
		return *a.LocationID
	},
}

type CloseInput struct {
	Resolution  string `json:"resolution" validate:"required,max=1000"`
	EvidenceURL string `json:"evidenceUrl,omitempty" validate:"omitempty,url"`
}

// ---------------------------------------------------------------------------
// Sensors
// ---------------------------------------------------------------------------

type Sensor struct {
	ID         string           `json:"id" db:"id"`
	LocationID string           `json:"locationId" db:"location_id"`
	Name       string           `json:"name" db:"name"`
	Kind       string           `json:"kind" db:"kind"`
	Unit       string           `json:"unit" db:"unit"`
	MinValue   *decimal.Decimal `json:"minValue" db:"min_value"`
	MaxValue   *decimal.Decimal `json:"maxValue" db:"max_value"`
	ExternalID *string          `json:"externalId" db:"external_id"`
	CreatedAt  time.Time        `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time        `json:"updatedAt" db:"updated_at"`
	ArchivedAt *time.Time       `json:"archivedAt" db:"archived_at"`
}

type SensorInput struct {
	LocationID string           `json:"locationId" db:"location_id" validate:"required"`
	Name       string           `json:"name" db:"name" validate:"required,max=100"`
	Kind       string           `json:"kind,omitempty" db:"kind" validate:"omitempty,max=50" doc:"temperature, humidity, door, meter…"`
	Unit       string           `json:"unit,omitempty" db:"unit" validate:"omitempty,max=20"`
	MinValue   *decimal.Decimal `json:"minValue,omitempty" db:"min_value"`
	MaxValue   *decimal.Decimal `json:"maxValue,omitempty" db:"max_value"`
	ExternalID *string          `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
}

var sensors = &crud.Resource[Sensor, SensorInput]{
	Path: "/sensors", Table: "sensors", Prefix: ids.Sensor, Noun: "sensor", Tag: tag,
	Feature: "operations.sensors", ReadScope: "operations:read", WriteScope: "operations:write", External: true, Archive: true,
	Filters:    []crud.Filter{{Query: "locationId", Column: "location_id"}},
	LocationOf: func(s *Sensor) string { return s.LocationID },
	Defaults: func(in *SensorInput) {
		if in.Kind == "" {
			in.Kind = "temperature"
		}
		if in.Unit == "" {
			in.Unit = "°C"
		}
	},
}

type ReadingInput struct {
	SensorID         string           `json:"sensorId,omitempty"`
	SensorExternalID string           `json:"sensorExternalId,omitempty"`
	At               time.Time        `json:"at" validate:"required"`
	Value            *decimal.Decimal `json:"value" validate:"required"`
}

type ReadingBatch struct {
	Source   string         `json:"source" validate:"required"`
	Readings []ReadingInput `json:"readings" validate:"required"`
}

type Reading struct {
	At      time.Time       `json:"at"`
	Value   decimal.Decimal `json:"value"`
	InRange bool            `json:"inRange"`
}

type readingList struct {
	SensorID string    `json:"sensorId"`
	Data     []Reading `json:"data"`
}

type subQuery struct {
	httpx.ListParams
	FormID     string `json:"formId,omitempty"`
	LocationID string `json:"locationId,omitempty"`
	Failed     bool   `json:"failed,omitempty" doc:"Only submissions with failed answers"`
}

func listSubs(c *httpx.Ctx, auditsOnly bool) (any, error) {
	lp, err := c.ParseList()
	if err != nil {
		return nil, err
	}
	rows, err := c.App.Pool.Query(c, `SELECT `+subCols+` FROM form_submissions s
		WHERE id > $1 AND ($2 = '' OR form_id = $2) AND ($3 = '' OR location_id = $3) AND (NOT $4 OR failed_count > 0)
		  AND (NOT $5 OR EXISTS (SELECT 1 FROM forms f WHERE f.id = s.form_id AND f.kind = 'audit'))
		ORDER BY id LIMIT $6`, lp.AfterID, c.Query("formId"), c.Query("locationId"), c.Query("failed") == "true", auditsOnly, lp.Limit+1)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Submission, error) { return scanSub(r) })
	if err != nil {
		return nil, err
	}
	return httpx.NewPage(list, lp.Limit, func(s Submission) string { return s.ID }), nil
}

// Routes returns the operations routes.
func Routes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "POST", Path: "/form-submissions", Tag: tag, Feature: feature, Scope: "operations:write",
			Summary:     "Submit a completed form or checklist",
			Description: "Answers are checked against each question. Failed answers raise form.answer_failed and, where configured, create corrective actions. Audits are scored.",
			Body:        SubmissionInput{}, Response: Submission{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in SubmissionInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Submission
				existing := false
				err := c.InTx(func(tx pgx.Tx) error {
					if in.Source != "" && in.ExternalID != "" {
						var id string
						err := tx.QueryRow(c, `SELECT id FROM form_submissions WHERE source=$1 AND external_id=$2`, in.Source, in.ExternalID).Scan(&id)
						if err == nil {
							existing = true
							out, err = getSub(c, tx, id)
							return err
						}
						if !errors.Is(err, pgx.ErrNoRows) {
							return err
						}
					}
					form, err := forms.Get(c, tx, "id", in.FormID, false)
					if err != nil {
						return httpx.Validation(httpx.FieldError{Path: "formId", Message: "Unknown form"})
					}
					if form.ArchivedAt != nil {
						return httpx.Validation(httpx.FieldError{Path: "formId", Message: "This form is archived"})
					}
					var locID, empID *string
					if in.LocationID != "" || in.LocationExternalID != "" {
						l, err := refs.Location(c, tx, in.LocationRef, "")
						if err != nil {
							return err
						}
						locID = &l
					}
					if e, err := refs.Employee(c, tx, in.EmployeeRef, "", true); err != nil {
						return err
					} else if e != "" {
						empID = &e
					}
					results, errs, failed, score, max, passed := evaluate(form, in.Answers)
					if len(errs) > 0 {
						return httpx.Validation(errs...)
					}
					at := time.Now()
					if in.SubmittedAt != nil {
						at = *in.SubmittedAt
					}
					var src, ext *string
					if in.Source != "" {
						src = &in.Source
					}
					if in.ExternalID != "" {
						ext = &in.ExternalID
					}
					out, err = scanSub(tx.QueryRow(c, `INSERT INTO form_submissions (id, form_id, form_version, location_id, employee_id, submitted_at,
						answers, results, failed_count, score, max_score, passed, source, external_id)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+subCols,
						ids.New(ids.Submission), form.ID, form.Version, locID, empID, at, in.Answers, results, failed, score, max, passed, src, ext))
					if err != nil {
						return err
					}
					loc := ""
					if locID != nil {
						loc = *locID
					}
					if err := c.Record(tx, httpx.Change{Action: "form.submit", EventType: "form.submitted", Feature: feature,
						EntityType: "form_submission", EntityID: out.ID, LocationID: loc, After: out}); err != nil {
						return err
					}
					actionsOn, err := c.App.Features.IsEnabled(c, "operations.corrective_actions")
					if err != nil {
						return err
					}
					byID := map[string]Question{}
					for _, q := range form.Questions {
						byID[q.ID] = q
					}
					for _, r := range results {
						if r.Passed == nil || *r.Passed {
							continue
						}
						q := byID[r.QuestionID]
						if err := c.Record(tx, httpx.Change{Action: "form.answer_failed", EventType: "form.answer_failed", Feature: feature,
							EntityType: "form_submission", EntityID: out.ID, LocationID: loc,
							After: map[string]any{"submissionId": out.ID, "formId": form.ID, "formName": form.Name, "question": q, "result": r, "locationId": locID}}); err != nil {
							return err
						}
						if q.CreateAction && actionsOn {
							due := httpx.NewDate(at.AddDate(0, 0, 1))
							qid, sid := q.ID, out.ID
							a, err := actions.Insert(c, tx, CorrectiveActionInput{LocationID: locID, Title: form.Name + ": " + q.Text, DueOn: &due})
							if err != nil {
								return err
							}
							if _, err := tx.Exec(c, `UPDATE corrective_actions SET submission_id=$2, question_id=$3 WHERE id=$1`, a.ID, sid, qid); err != nil {
								return err
							}
						}
					}
					if form.Kind == "audit" {
						return c.Record(tx, httpx.Change{Action: "audit.complete", EventType: "audit.completed", Feature: "operations.audits",
							EntityType: "form_submission", EntityID: out.ID, LocationID: loc, After: out})
					}
					return nil
				})
				if err != nil {
					return nil, err
				}
				if existing {
					return httpx.Result{Status: 200, Body: out}, nil
				}
				return out, nil
			},
		},
		{
			Method: "GET", Path: "/form-submissions", Tag: tag, Feature: feature, Scope: "operations:read",
			Summary: "List submissions", Query: subQuery{}, Response: httpx.Page[Submission]{},
			Handler: func(c *httpx.Ctx) (any, error) { return listSubs(c, false) },
		},
		{
			Method: "GET", Path: "/form-submissions/{id}", Tag: tag, Feature: feature, Scope: "operations:read",
			Summary: "Get a submission with per-question results", Response: Submission{},
			Handler: func(c *httpx.Ctx) (any, error) { return getSub(c, c.App.Pool, c.Param("id")) },
		},
		{
			Method: "GET", Path: "/audits", Tag: tag, Feature: "operations.audits", Scope: "operations:read",
			Summary: "List scored audits", Query: subQuery{}, Response: httpx.Page[Submission]{},
			Handler: func(c *httpx.Ctx) (any, error) { return listSubs(c, true) },
		},
		{
			Method: "POST", Path: "/corrective-actions/{id}:close", Tag: tag, Feature: "operations.corrective_actions", Scope: "operations:write",
			Summary: "Close a corrective action with its resolution", Body: CloseInput{}, Response: CorrectiveAction{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in CloseInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out CorrectiveAction
				err := c.InTx(func(tx pgx.Tx) error {
					before, err := actions.Get(c, tx, "id", c.Param("id"), true)
					if err != nil {
						return err
					}
					if before.Status == "closed" {
						return httpx.Conflict("This corrective action is already closed.")
					}
					if _, err := tx.Exec(c, `UPDATE corrective_actions SET status='closed', resolution=$2, evidence_url=nullif($3,''),
						closed_at=now(), version=version+1, updated_at=now() WHERE id=$1`, before.ID, in.Resolution, in.EvidenceURL); err != nil {
						return err
					}
					out, err = actions.Get(c, tx, "id", before.ID, false)
					if err != nil {
						return err
					}
					loc := ""
					if out.LocationID != nil {
						loc = *out.LocationID
					}
					return c.Record(tx, httpx.Change{Action: "corrective_action.close", EventType: "corrective_action.closed",
						Feature: "operations.corrective_actions", EntityType: "corrective_action", EntityID: out.ID, LocationID: loc, Before: before, After: out})
				})
				return out, err
			},
		},
		{
			Method: "POST", Path: "/sensor-readings", Tag: tag, Feature: "operations.sensors", Scope: "operations:write",
			Ingest: true, Status: 202, Summary: "Send sensor readings",
			Description: "Each reading is checked against the sensor's range. When a sensor goes out of range, sensor.out_of_range is raised once until it returns to range.",
			Body:        ReadingBatch{}, Response: httpx.BatchResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return ingest.Run(c, "sensor_readings", "readings", func(e *ingest.Env, _ int, in ReadingInput) (ingest.Outcome, error) {
					var s Sensor
					var err error
					switch {
					case in.SensorID != "":
						s, err = sensors.Get(c, e.Tx, "id", in.SensorID, false)
					case in.SensorExternalID != "":
						s, err = sensors.Get(c, e.Tx, "external_id", in.SensorExternalID, false)
					default:
						return ingest.Outcome{}, ingest.Rejectf("sensorId", "Set sensorId or sensorExternalId")
					}
					if err != nil {
						return ingest.Outcome{}, ingest.Rejectf("sensorId", "Unknown sensor")
					}
					inRange := (s.MinValue == nil || !in.Value.LessThan(*s.MinValue)) && (s.MaxValue == nil || !in.Value.GreaterThan(*s.MaxValue))
					var prevInRange *bool
					_ = e.Tx.QueryRow(c, `SELECT in_range FROM sensor_readings WHERE sensor_id=$1 AND at < $2 ORDER BY at DESC LIMIT 1`, s.ID, in.At).Scan(&prevInRange)
					var inserted bool
					err = e.Tx.QueryRow(c, `INSERT INTO sensor_readings (sensor_id, at, value, in_range) VALUES ($1,$2,$3,$4)
						ON CONFLICT (sensor_id, at) DO UPDATE SET value=EXCLUDED.value, in_range=EXCLUDED.in_range
						RETURNING (xmax = 0)`, s.ID, in.At, *in.Value, inRange).Scan(&inserted)
					if err != nil {
						return ingest.Outcome{}, err
					}
					if !inRange && (prevInRange == nil || *prevInRange) {
						if err := c.Record(e.Tx, httpx.Change{Action: "sensor.out_of_range", EventType: "sensor.out_of_range", Feature: "operations.sensors",
							EntityType: "sensor", EntityID: s.ID, LocationID: s.LocationID,
							After: map[string]any{"sensor": s, "at": in.At, "value": in.Value}}); err != nil {
							return ingest.Outcome{}, err
						}
					}
					return ingest.Outcome{Status: ingest.Status(inserted), ID: s.ID}, nil
				})
			},
		},
		{
			Method: "GET", Path: "/sensors/{id}/readings", Tag: tag, Feature: "operations.sensors", Scope: "operations:read",
			Summary: "Recent readings for a sensor (newest first, up to 1,000)", Response: readingList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				if _, err := sensors.Get(c, c.App.Pool, "id", c.Param("id"), false); err != nil {
					return nil, err
				}
				rows, err := c.App.Pool.Query(c, `SELECT at, value, in_range FROM sensor_readings WHERE sensor_id=$1 ORDER BY at DESC LIMIT 1000`, c.Param("id"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Reading, error) {
					var rd Reading
					return rd, r.Scan(&rd.At, &rd.Value, &rd.InRange)
				})
				if list == nil {
					list = []Reading{}
				}
				return readingList{SensorID: c.Param("id"), Data: list}, err
			},
		},
	}
	routes = append(routes, forms.Routes()...)
	routes = append(routes, actions.Routes()...)
	routes = append(routes, sensors.Routes()...)
	return routes
}
