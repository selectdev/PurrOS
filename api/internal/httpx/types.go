package httpx

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Date is a calendar date without time zone, serialized as "YYYY-MM-DD".
type Date struct{ time.Time }

const dateLayout = "2006-01-02"

func NewDate(t time.Time) Date {
	return Date{time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)}
}

func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("must be a date in YYYY-MM-DD format")
	}
	return Date{t}, nil
}

func (d Date) String() string { return d.Format(dateLayout) }

func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("must be a date in YYYY-MM-DD format")
	}
	parsed, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// ScanDate implements pgtype.DateScanner.
func (d *Date) ScanDate(v pgtype.Date) error {
	if !v.Valid {
		*d = Date{}
		return nil
	}
	*d = NewDate(v.Time)
	return nil
}

// DateValue implements pgtype.DateValuer.
func (d Date) DateValue() (pgtype.Date, error) {
	return pgtype.Date{Time: d.Time, Valid: !d.IsZero()}, nil
}

// TimeOfDay is a wall-clock time serialized as "HH:MM".
type TimeOfDay struct{ Minutes int }

func ParseTimeOfDay(s string) (TimeOfDay, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return TimeOfDay{}, fmt.Errorf("must be a time in HH:MM format")
	}
	return TimeOfDay{Minutes: t.Hour()*60 + t.Minute()}, nil
}

func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Minutes/60, t.Minutes%60) }

func (t TimeOfDay) MarshalJSON() ([]byte, error) { return json.Marshal(t.String()) }

func (t *TimeOfDay) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("must be a time in HH:MM format")
	}
	v, err := ParseTimeOfDay(s)
	if err != nil {
		return err
	}
	*t = v
	return nil
}

// ScanTime implements pgtype.TimeScanner.
func (t *TimeOfDay) ScanTime(v pgtype.Time) error {
	*t = TimeOfDay{Minutes: int(v.Microseconds / 60_000_000)}
	return nil
}

// TimeValue implements pgtype.TimeValuer.
func (t TimeOfDay) TimeValue() (pgtype.Time, error) {
	return pgtype.Time{Microseconds: int64(t.Minutes) * 60_000_000, Valid: true}, nil
}
