package httpx

import (
	"encoding/json"
	"errors"
)

// MergePatch overlays the request's JSON fields onto current (JSON merge
// semantics at the top level): absent fields are kept, explicit nulls clear
// optional fields. The result is validated.
func MergePatch[T any](c *Ctx, current T) (T, error) {
	var out T
	var patch map[string]json.RawMessage
	if err := c.Decode(&patch); err != nil {
		return out, err
	}
	base, err := json.Marshal(current)
	if err != nil {
		return out, err
	}
	merged := map[string]json.RawMessage{}
	_ = json.Unmarshal(base, &merged)
	for k, v := range patch {
		merged[k] = v
	}
	b, _ := json.Marshal(merged)
	if err := json.Unmarshal(b, &out); err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			return out, Validation(FieldError{Path: ute.Field, Message: "Must be of type " + ute.Type.String()})
		}
		return out, BadRequest(err.Error())
	}
	return out, Validate(out)
}
