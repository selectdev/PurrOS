package httpx

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/shopspring/decimal"
)

var validate = func() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			return ""
		}
		if name == "" {
			return f.Name
		}
		return name
	})
	// Decimals: "dpos" = greater than zero, "dnonneg" = zero or more.
	v.RegisterCustomTypeFunc(func(field reflect.Value) any {
		if d, ok := field.Interface().(decimal.Decimal); ok {
			return d.String()
		}
		return nil
	}, decimal.Decimal{})
	_ = v.RegisterValidation("dpos", func(fl validator.FieldLevel) bool {
		d, err := decimal.NewFromString(fl.Field().String())
		return err == nil && d.IsPositive()
	})
	_ = v.RegisterValidation("dnonneg", func(fl validator.FieldLevel) bool {
		d, err := decimal.NewFromString(fl.Field().String())
		return err == nil && !d.IsNegative()
	})
	_ = v.RegisterValidation("currency", func(fl validator.FieldLevel) bool {
		return currencyRe.MatchString(fl.Field().String())
	})
	_ = v.RegisterValidation("extid", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		return len(s) <= 200 && !strings.ContainsAny(s, "\x00/")
	})
	return v
}()

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// Validate checks struct tags and returns a validation Problem listing every
// invalid field by its JSON path.
func Validate(v any) error {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil // maps and raw values are validated by their callers
	}
	err := validate.Struct(v)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return BadRequest(err.Error())
	}
	out := make([]FieldError, 0, len(verrs))
	for _, fe := range verrs {
		out = append(out, FieldError{Path: jsonPath(fe.Namespace()), Message: message(fe)})
	}
	return Validation(out...)
}

// jsonPath drops the root struct name: "punchBatch.punches[0].type" → "punches[0].type".
func jsonPath(ns string) string {
	if _, rest, ok := strings.Cut(ns, "."); ok {
		return rest
	}
	return ns
}

func message(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required", "required_without", "required_without_all":
		return "Required"
	case "oneof":
		return "Must be one of: " + strings.ReplaceAll(fe.Param(), " ", ", ")
	case "max":
		if fe.Kind() == reflect.Slice {
			return fmt.Sprintf("At most %s items", fe.Param())
		}
		return fmt.Sprintf("At most %s characters", fe.Param())
	case "min":
		if fe.Kind() == reflect.Slice {
			return fmt.Sprintf("At least %s items", fe.Param())
		}
		return fmt.Sprintf("At least %s characters", fe.Param())
	case "email":
		return "Must be a valid email address"
	case "url", "http_url":
		return "Must be a valid URL"
	case "dpos":
		return "Must be a decimal greater than zero"
	case "dnonneg":
		return "Must be a decimal of zero or more"
	case "currency":
		return "Must be a 3-letter ISO 4217 currency code"
	case "extid":
		return "Must be at most 200 characters and not contain '/'"
	case "gtfield":
		p := fe.Param()
		return "Must be after " + strings.ToLower(p[:1]) + p[1:]
	default:
		return "Invalid (" + fe.Tag() + ")"
	}
}
