package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

// FieldError points at one invalid field, using its JSON path
// (e.g. "lines[0].quantity").
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Problem is an RFC 9457 problem-details error with a stable machine-readable code.
type Problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Code      string       `json:"code"`
	Detail    string       `json:"detail,omitempty"`
	RequestID string       `json:"requestId,omitempty"`
	Errors    []FieldError `json:"errors,omitempty"`

	retryAfter int // seconds, for 429
}

func (p *Problem) Error() string {
	if p.Detail != "" {
		return fmt.Sprintf("%s: %s", p.Code, p.Detail)
	}
	return p.Code
}

func newProblem(status int, code, title, detail string) *Problem {
	return &Problem{
		Type:   "https://purros.dev/errors/" + code,
		Title:  title,
		Status: status,
		Code:   code,
		Detail: detail,
	}
}

func BadRequest(detail string) *Problem {
	return newProblem(http.StatusBadRequest, "bad_request", "Bad request", detail)
}

func Unauthorized(detail string) *Problem {
	return newProblem(http.StatusUnauthorized, "unauthorized", "Unauthorized", detail)
}

func Forbidden(detail string) *Problem {
	return newProblem(http.StatusForbidden, "forbidden", "Forbidden", detail)
}

func NotFound(detail string) *Problem {
	return newProblem(http.StatusNotFound, "not_found", "Not found", detail)
}

func FeatureDisabled(feature string) *Problem {
	return newProblem(http.StatusNotFound, "feature_disabled", "Feature disabled",
		fmt.Sprintf("The %q feature is switched off.", feature))
}

func MethodNotAllowed() *Problem {
	return newProblem(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", "")
}

func Conflict(detail string) *Problem {
	return newProblem(http.StatusConflict, "conflict", "Conflict", detail)
}

func VersionMismatch() *Problem {
	return newProblem(http.StatusConflict, "version_mismatch", "Version mismatch",
		"The record was changed by someone else. Fetch it again and retry.")
}

func PeriodLocked(detail string) *Problem {
	return newProblem(http.StatusConflict, "period_locked", "Period locked", detail)
}

func Validation(errs ...FieldError) *Problem {
	p := newProblem(http.StatusUnprocessableEntity, "validation_error", "Validation failed", "")
	p.Errors = errs
	return p
}

func RateLimited(retryAfter int) *Problem {
	p := newProblem(http.StatusTooManyRequests, "rate_limited", "Too many requests",
		"Rate limit exceeded. Slow down or use batch endpoints.")
	p.retryAfter = retryAfter
	return p
}

func Internal() *Problem {
	return newProblem(http.StatusInternalServerError, "internal_error", "Internal error",
		"Something went wrong. Retry with backoff, and quote the requestId if you report it.")
}

// AsProblem converts any error into a Problem. Unknown errors become 500s.
func AsProblem(err error) (*Problem, bool) {
	var p *Problem
	if errors.As(err, &p) {
		return p, true
	}
	return Internal(), false
}
