// Package communication serves announcements, the company calendar,
// recognitions and team display metrics.
package communication

import (
	"strconv"
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

const tag = "Communication"

// ---------------------------------------------------------------------------
// Announcements
// ---------------------------------------------------------------------------

type Announcement struct {
	ID          string     `json:"id" db:"id"`
	Title       string     `json:"title" db:"title"`
	Body        string     `json:"body" db:"body"`
	LocationIDs []string   `json:"locationIds" db:"location_ids" doc:"Empty means everyone"`
	RequireAck  bool       `json:"requireAck" db:"require_ack"`
	PublishAt   time.Time  `json:"publishAt" db:"publish_at"`
	ExpiresAt   *time.Time `json:"expiresAt" db:"expires_at"`
	CreatedAt   time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time  `json:"updatedAt" db:"updated_at"`
	ArchivedAt  *time.Time `json:"archivedAt" db:"archived_at"`
}

type AnnouncementInput struct {
	Title       string     `json:"title" db:"title" validate:"required,max=200"`
	Body        string     `json:"body" db:"body" validate:"required,max=20000"`
	LocationIDs []string   `json:"locationIds,omitempty" db:"location_ids"`
	RequireAck  bool       `json:"requireAck,omitempty" db:"require_ack" doc:"Staff must acknowledge (policies)"`
	PublishAt   *time.Time `json:"publishAt,omitempty" db:"publish_at" doc:"Defaults to now"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty" db:"expires_at"`
}

var announcements = &crud.Resource[Announcement, AnnouncementInput]{
	Path: "/announcements", Table: "announcements", Prefix: ids.Announcement, Noun: "announcement", Tag: tag,
	Feature: "communication.announcements", ReadScope: "communication:read", WriteScope: "communication:write", Archive: true,
	CreatedEvent: "announcement.published",
	Check: func(c *httpx.Ctx, q db.Querier, in *AnnouncementInput, before *Announcement) error {
		if in.LocationIDs == nil {
			in.LocationIDs = []string{}
		}
		if in.PublishAt == nil {
			now := time.Now()
			if before != nil {
				now = before.PublishAt
			}
			in.PublishAt = &now
		}
		if in.ExpiresAt != nil && !in.ExpiresAt.After(*in.PublishAt) {
			return httpx.Validation(httpx.FieldError{Path: "expiresAt", Message: "Must be after publishAt"})
		}
		for i, l := range in.LocationIDs {
			if _, err := refs.Location(c, q, refs.LocationRef{LocationID: l}, ""); err != nil {
				return httpx.Validation(httpx.FieldError{Path: "locationIds[" + strconv.Itoa(i) + "]", Message: "Unknown location"})
			}
		}
		return nil
	},
}

type AckInput struct {
	refs.EmployeeRef
}

type Ack struct {
	EmployeeID string    `json:"employeeId"`
	AckedAt    time.Time `json:"ackedAt"`
}

type ackList struct {
	AnnouncementID string `json:"announcementId"`
	Count          int    `json:"count"`
	Data           []Ack  `json:"data"`
}

func loadAcks(c *httpx.Ctx, q db.Querier, id string) (ackList, error) {
	rows, err := q.Query(c, `SELECT employee_id, acked_at FROM announcement_acks WHERE announcement_id=$1 ORDER BY acked_at`, id)
	if err != nil {
		return ackList{}, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Ack, error) {
		var a Ack
		return a, r.Scan(&a.EmployeeID, &a.AckedAt)
	})
	if list == nil {
		list = []Ack{}
	}
	return ackList{AnnouncementID: id, Count: len(list), Data: list}, err
}

// ---------------------------------------------------------------------------
// Calendar
// ---------------------------------------------------------------------------

type CalendarEvent struct {
	ID          string     `json:"id" db:"id"`
	Title       string     `json:"title" db:"title"`
	Description *string    `json:"description" db:"description"`
	LocationID  *string    `json:"locationId" db:"location_id"`
	Kind        string     `json:"kind" db:"kind"`
	StartsAt    time.Time  `json:"startsAt" db:"starts_at"`
	EndsAt      time.Time  `json:"endsAt" db:"ends_at"`
	AllDay      bool       `json:"allDay" db:"all_day"`
	ExternalID  *string    `json:"externalId" db:"external_id"`
	CreatedAt   time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time  `json:"updatedAt" db:"updated_at"`
	ArchivedAt  *time.Time `json:"archivedAt" db:"archived_at"`
}

type CalendarEventInput struct {
	Title       string    `json:"title" db:"title" validate:"required,max=200"`
	Description *string   `json:"description,omitempty" db:"description" validate:"omitempty,max=4000"`
	LocationID  *string   `json:"locationId,omitempty" db:"location_id"`
	Kind        string    `json:"kind,omitempty" db:"kind" validate:"omitempty,max=50" doc:"event, delivery, inspection, promotion, holiday…"`
	StartsAt    time.Time `json:"startsAt" db:"starts_at" validate:"required"`
	EndsAt      time.Time `json:"endsAt" db:"ends_at" validate:"required,gtefield=StartsAt"`
	AllDay      bool      `json:"allDay,omitempty" db:"all_day"`
	ExternalID  *string   `json:"externalId,omitempty" db:"external_id" validate:"omitempty,extid"`
}

var calendar = &crud.Resource[CalendarEvent, CalendarEventInput]{
	Path: "/calendar-events", Table: "calendar_events", Prefix: ids.CalendarEvent, Noun: "calendar_event", Tag: tag,
	Feature: "communication.calendar", ReadScope: "communication:read", WriteScope: "communication:write",
	External: true, Archive: true,
	Filters: []crud.Filter{{Query: "locationId", Column: "location_id"}, {Query: "kind", Column: "kind"}},
	Defaults: func(in *CalendarEventInput) {
		if in.Kind == "" {
			in.Kind = "event"
		}
	},
}

// ---------------------------------------------------------------------------
// Recognition and display metrics
// ---------------------------------------------------------------------------

type Recognition struct {
	ID         string    `json:"id"`
	EmployeeID string    `json:"employeeId"`
	LocationID *string   `json:"locationId"`
	Message    string    `json:"message"`
	GivenBy    *string   `json:"givenBy"`
	CreatedAt  time.Time `json:"createdAt"`
}

type RecognitionInput struct {
	refs.EmployeeRef
	LocationID string `json:"locationId,omitempty"`
	Message    string `json:"message" validate:"required,max=500"`
	GivenBy    string `json:"givenBy,omitempty" validate:"max=100"`
}

type recognitionList struct {
	Data []Recognition `json:"data"`
}

type MetricInput struct {
	refs.LocationRef
	Key    string           `json:"key" validate:"required,max=50"`
	Label  string           `json:"label" validate:"required,max=100"`
	Value  *decimal.Decimal `json:"value" validate:"required"`
	Unit   string           `json:"unit,omitempty" validate:"max=20"`
	Target *decimal.Decimal `json:"target,omitempty"`
}

type MetricBatch struct {
	Source  string        `json:"source" validate:"required"`
	Metrics []MetricInput `json:"metrics" validate:"required"`
}

type Metric struct {
	LocationID string           `json:"locationId"`
	Key        string           `json:"key"`
	Label      string           `json:"label"`
	Value      decimal.Decimal  `json:"value"`
	Unit       *string          `json:"unit"`
	Target     *decimal.Decimal `json:"target"`
	UpdatedAt  time.Time        `json:"updatedAt"`
}

type metricList struct {
	Data []Metric `json:"data"`
}

// Routes returns the communication and display routes.
func Routes() []httpx.Route {
	routes := []httpx.Route{
		{
			Method: "POST", Path: "/announcements/{id}:acknowledge", Tag: tag, Feature: "communication.announcements", Scope: "communication:write",
			Summary: "Record that an employee read an announcement", Body: AckInput{}, Response: ackList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				var in AckInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out ackList
				err := c.InTx(func(tx pgx.Tx) error {
					a, err := announcements.Get(c, tx, "id", c.Param("id"), false)
					if err != nil {
						return err
					}
					emp, err := refs.Employee(c, tx, in.EmployeeRef, "", false)
					if err != nil {
						return err
					}
					if _, err := tx.Exec(c, `INSERT INTO announcement_acks (announcement_id, employee_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, a.ID, emp); err != nil {
						return err
					}
					out, err = loadAcks(c, tx, a.ID)
					return err
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/announcements/{id}/acknowledgments", Tag: tag, Feature: "communication.announcements", Scope: "communication:read",
			Summary: "Who has acknowledged an announcement", Response: ackList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				if _, err := announcements.Get(c, c.App.Pool, "id", c.Param("id"), false); err != nil {
					return nil, err
				}
				return loadAcks(c, c.App.Pool, c.Param("id"))
			},
		},
		{
			Method: "POST", Path: "/recognitions", Tag: "Team Displays", Feature: "displays.gamification", Scope: "communication:write",
			Summary: "Post a shout-out", Body: RecognitionInput{}, Response: Recognition{}, Status: 201,
			Handler: func(c *httpx.Ctx) (any, error) {
				var in RecognitionInput
				if err := c.Decode(&in); err != nil {
					return nil, err
				}
				var out Recognition
				err := c.InTx(func(tx pgx.Tx) error {
					emp, err := refs.Employee(c, tx, in.EmployeeRef, "", false)
					if err != nil {
						return err
					}
					var loc *string
					if in.LocationID != "" {
						l, err := refs.Location(c, tx, refs.LocationRef{LocationID: in.LocationID}, "")
						if err != nil {
							return err
						}
						loc = &l
					}
					err = tx.QueryRow(c, `INSERT INTO recognitions (id, employee_id, location_id, message, given_by) VALUES ($1,$2,$3,$4,nullif($5,''))
						RETURNING id, employee_id, location_id, message, given_by, created_at`,
						ids.New(ids.Recognition), emp, loc, in.Message, in.GivenBy).
						Scan(&out.ID, &out.EmployeeID, &out.LocationID, &out.Message, &out.GivenBy, &out.CreatedAt)
					if err != nil {
						return err
					}
					l := ""
					if loc != nil {
						l = *loc
					}
					return c.Record(tx, httpx.Change{Action: "recognition.post", EventType: "recognition.posted", Feature: "displays.gamification",
						EntityType: "recognition", EntityID: out.ID, LocationID: l, After: out})
				})
				return out, err
			},
		},
		{
			Method: "GET", Path: "/recognitions", Tag: "Team Displays", Feature: "displays.gamification", Scope: "communication:read",
			Summary: "Recent shout-outs (newest first)", Response: recognitionList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT id, employee_id, location_id, message, given_by, created_at FROM recognitions
					WHERE $1 = '' OR location_id = $1 ORDER BY created_at DESC LIMIT 100`, c.Query("locationId"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Recognition, error) {
					var x Recognition
					return x, r.Scan(&x.ID, &x.EmployeeID, &x.LocationID, &x.Message, &x.GivenBy, &x.CreatedAt)
				})
				if list == nil {
					list = []Recognition{}
				}
				return recognitionList{Data: list}, err
			},
		},
		{
			Method: "POST", Path: "/display-metrics", Tag: "Team Displays", Feature: "displays", Scope: "reports:write",
			Ingest: true, Status: 202, Summary: "Push live numbers to team displays",
			Description: "For figures PurrOS doesn't calculate itself, e.g. average service time. Upserted by location and key.",
			Body:        MetricBatch{}, Response: httpx.BatchResult{},
			Handler: func(c *httpx.Ctx) (any, error) {
				return ingest.Run(c, "display_metrics", "metrics", func(e *ingest.Env, _ int, in MetricInput) (ingest.Outcome, error) {
					loc, err := e.Locs.Resolve(c, in.LocationID, in.LocationExternalID)
					if err != nil {
						return ingest.Outcome{}, ingest.Rejectf("location", "%s", err.Error())
					}
					var inserted bool
					err = e.Tx.QueryRow(c, `INSERT INTO display_metrics (location_id, key, label, value, unit, target) VALUES ($1,$2,$3,$4,nullif($5,''),$6)
						ON CONFLICT (location_id, key) DO UPDATE SET label=EXCLUDED.label, value=EXCLUDED.value, unit=EXCLUDED.unit,
							target=EXCLUDED.target, updated_at=now()
						RETURNING (xmax = 0)`, loc.ID, in.Key, in.Label, *in.Value, in.Unit, in.Target).Scan(&inserted)
					return ingest.Outcome{Status: ingest.Status(inserted), ID: loc.ID + ":" + in.Key}, err
				})
			},
		},
		{
			Method: "GET", Path: "/display-metrics", Tag: "Team Displays", Feature: "displays", Scope: "reports:read",
			Summary: "Live display metrics", Response: metricList{},
			Handler: func(c *httpx.Ctx) (any, error) {
				rows, err := c.App.Pool.Query(c, `SELECT location_id, key, label, value, unit, target, updated_at FROM display_metrics
					WHERE $1 = '' OR location_id = $1 ORDER BY location_id, key`, c.Query("locationId"))
				if err != nil {
					return nil, err
				}
				list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Metric, error) {
					var m Metric
					return m, r.Scan(&m.LocationID, &m.Key, &m.Label, &m.Value, &m.Unit, &m.Target, &m.UpdatedAt)
				})
				if list == nil {
					list = []Metric{}
				}
				return metricList{Data: list}, err
			},
		},
	}
	routes = append(routes, announcements.Routes()...)
	routes = append(routes, calendar.Routes()...)
	return routes
}
