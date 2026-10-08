package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// RangeValue is the value of a range field: the item fits every N in
// [Min, Max].
type RangeValue struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// Contains reports whether n lies in [Min, Max].
func (r RangeValue) Contains(n float64) bool { return n >= r.Min && n <= r.Max }

// Value is one decoded field value. Exactly the member matching the field's
// type is set.
type Value struct {
	Type     Type
	Category string
	Bool     bool
	Set      []string
	Number   float64
	Range    RangeValue
	// Votes holds a count per value.
	Votes map[string]float64
}

// Values returns the value or values the item holds for affinity and
// narrowing: the category, the set members, or "true"/"false" for a bool.
// It returns nil for number, range and votes, which are compared otherwise.
func (v Value) Values() []string {
	switch v.Type {
	case Category:
		return []string{v.Category}
	case Bool:
		if v.Bool {
			return []string{"true"}
		}
		return []string{"false"}
	case Set:
		return v.Set
	default:
		return nil
	}
}

// Shares returns each vote value's share of the total, or nil when the
// field is not votes or holds no votes.
func (v Value) Shares() map[string]float64 {
	if v.Type != Votes {
		return nil
	}
	var total float64
	for _, n := range v.Votes {
		total += n
	}
	if total == 0 {
		return nil
	}
	shares := make(map[string]float64, len(v.Votes))
	for k, n := range v.Votes {
		shares[k] = n / total
	}
	return shares
}

var errNull = errors.New("null value")

// Decode parses raw JSON as a value of the field's type. JSON null is an
// error: an item without a value omits the field.
func (f Field) Decode(raw json.RawMessage) (Value, error) {
	v := Value{Type: f.Type}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return v, fmt.Errorf("field %q: %w", f.Name, errNull)
	}
	var err error
	switch f.Type {
	case Category:
		err = strict(raw, &v.Category)
		if err == nil && v.Category == "" {
			err = errors.New("empty category")
		}
	case Bool:
		err = strict(raw, &v.Bool)
	case Set:
		err = strict(raw, &v.Set)
		if err == nil {
			err = checkSet(v.Set)
		}
		if err == nil {
			slices.Sort(v.Set)
		}
	case Number:
		err = strict(raw, &v.Number)
	case Range:
		err = strict(raw, &v.Range)
		if err == nil && v.Range.Min > v.Range.Max {
			err = fmt.Errorf("min %v is above max %v", v.Range.Min, v.Range.Max)
		}
	case Votes:
		err = strict(raw, &v.Votes)
		if err == nil {
			err = checkVotes(v.Votes)
		}
	default:
		err = fmt.Errorf("unknown type %q", f.Type)
	}
	if err != nil {
		return Value{}, fmt.Errorf("field %q: %w", f.Name, err)
	}
	return v, nil
}

func strict(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

func checkSet(set []string) error {
	seen := make(map[string]bool, len(set))
	for _, s := range set {
		if s == "" {
			return errors.New("empty set member")
		}
		if seen[s] {
			return fmt.Errorf("set member %q repeated", s)
		}
		seen[s] = true
	}
	return nil
}

func checkVotes(votes map[string]float64) error {
	for k, n := range votes {
		if k == "" {
			return errors.New("empty vote value")
		}
		if n < 0 {
			return fmt.Errorf("negative vote count for %q", k)
		}
	}
	return nil
}

// MarshalJSON writes the value in the form Decode reads.
func (v Value) MarshalJSON() ([]byte, error) {
	switch v.Type {
	case Category:
		return json.Marshal(v.Category)
	case Bool:
		return json.Marshal(v.Bool)
	case Set:
		if v.Set == nil {
			return []byte("[]"), nil
		}
		return json.Marshal(v.Set)
	case Number:
		return json.Marshal(v.Number)
	case Range:
		return json.Marshal(v.Range)
	case Votes:
		if v.Votes == nil {
			return []byte("{}"), nil
		}
		return json.Marshal(v.Votes)
	default:
		return nil, fmt.Errorf("unknown type %q", v.Type)
	}
}
