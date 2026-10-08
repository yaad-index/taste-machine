// Package schema declares the fields a catalogue or taste file carries and
// validates both the declarations and the values stored against them.
package schema

import (
	"errors"
	"fmt"
	"slices"
)

// Type is the kind of value a field holds.
type Type string

const (
	// Category is one value out of a set.
	Category Type = "category"
	// Bool is true or false.
	Bool Type = "bool"
	// Set is any number of values.
	Set Type = "set"
	// Number is a numeric value, compared through buckets.
	Number Type = "number"
	// Range is a min and a max, such as the counts an item fits.
	Range Type = "range"
	// Votes is a count per value, such as a poll.
	Votes Type = "votes"
)

// Role says how the engine uses a field.
type Role string

const (
	// Filter fields exclude items that fail them.
	Filter Role = "filter"
	// Preference fields order the items that pass the filters.
	Preference Role = "preference"
	// Both fields act as a filter and as a preference.
	Both Role = "both"
	// Info fields are shown but never scored or filtered.
	Info Role = "info"
)

// Missing says what a filter on a field does with an item that lacks it.
type Missing string

const (
	// Keep lets an item without the field pass. It is the default.
	Keep Missing = "keep"
	// Drop excludes an item without the field.
	Drop Missing = "drop"
)

// DefaultBuckets is the number of quantile buckets for a number field that
// declares neither edges nor a count.
const DefaultBuckets = 5

// Field is one declared field.
type Field struct {
	Name    string `json:"name"`
	Type    Type   `json:"type"`
	Role    Role   `json:"role"`
	Askable bool   `json:"askable,omitempty"`
	// Weight scales the field in the taste score and in similarity. Zero
	// means unset and counts as 1; a field that should not count at all
	// takes role info instead.
	Weight  float64 `json:"weight,omitempty"`
	Missing Missing `json:"missing,omitempty"`
	// Edges are declared bucket edges for a number field, ascending.
	Edges []float64 `json:"edges,omitempty"`
	// Buckets is the quantile bucket count for a number field without edges.
	Buckets int `json:"buckets,omitempty"`
	// GroupSize marks the range field checked against the group size.
	GroupSize bool `json:"group_size,omitempty"`
	// Display marks the info category field shown next to an item's id.
	Display bool `json:"display,omitempty"`
}

// Schema is the ordered list of fields a file declares. The order is the
// schema order used to break ties.
type Schema struct {
	Fields []Field `json:"fields"`
}

// allowedRoles lists, per type, the roles it may take. A nil entry allows
// every role.
var allowedRoles = map[Type][]Role{
	Category: nil,
	Bool:     nil,
	Set:      nil,
	Number:   nil,
	Range:    {Filter, Info},
	Votes:    {Preference, Info},
}

// Validate checks every field declaration. It returns all problems found,
// joined, so one load reports everything that is wrong.
func (s Schema) Validate() error {
	var errs []error
	seen := make(map[string]bool, len(s.Fields))
	groupSize, display := 0, 0
	for i, f := range s.Fields {
		if f.Name == "" {
			errs = append(errs, fmt.Errorf("field %d: empty name", i))
			continue
		}
		if seen[f.Name] {
			errs = append(errs, fmt.Errorf("field %q: declared twice", f.Name))
		}
		seen[f.Name] = true
		errs = append(errs, f.validate()...)
		if f.GroupSize {
			groupSize++
		}
		if f.Display {
			display++
		}
	}
	if groupSize > 1 {
		errs = append(errs, fmt.Errorf("group_size is set on %d fields, at most one is allowed", groupSize))
	}
	if display > 1 {
		errs = append(errs, fmt.Errorf("display is set on %d fields, at most one is allowed", display))
	}
	return errors.Join(errs...)
}

func (f Field) validate() []error {
	var errs []error
	roles, ok := allowedRoles[f.Type]
	if !ok {
		return []error{fmt.Errorf("field %q: unknown type %q", f.Name, f.Type)}
	}
	switch f.Role {
	case Filter, Preference, Both, Info:
		if roles != nil && !slices.Contains(roles, f.Role) {
			errs = append(errs, fmt.Errorf("field %q: type %s cannot have role %s", f.Name, f.Type, f.Role))
		}
	default:
		errs = append(errs, fmt.Errorf("field %q: unknown role %q", f.Name, f.Role))
	}
	switch f.Missing {
	case "", Keep, Drop:
	default:
		errs = append(errs, fmt.Errorf("field %q: unknown missing policy %q", f.Name, f.Missing))
	}
	if f.Weight < 0 {
		errs = append(errs, fmt.Errorf("field %q: negative weight", f.Name))
	}
	if f.Type != Number && (len(f.Edges) > 0 || f.Buckets != 0) {
		errs = append(errs, fmt.Errorf("field %q: bucket settings on a %s field", f.Name, f.Type))
	}
	if len(f.Edges) > 0 && f.Buckets != 0 {
		errs = append(errs, fmt.Errorf("field %q: both edges and a bucket count", f.Name))
	}
	if f.Buckets < 0 {
		errs = append(errs, fmt.Errorf("field %q: negative bucket count", f.Name))
	}
	for i := 1; i < len(f.Edges); i++ {
		if f.Edges[i] <= f.Edges[i-1] {
			errs = append(errs, fmt.Errorf("field %q: edges must be strictly ascending", f.Name))
			break
		}
	}
	if f.GroupSize && f.Type != Range {
		errs = append(errs, fmt.Errorf("field %q: group_size on a %s field, only range fields may carry it", f.Name, f.Type))
	}
	if f.Display && (f.Type != Category || f.Role != Info) {
		errs = append(errs, fmt.Errorf("field %q: display is allowed on info category fields only, this is %s %s", f.Name, f.Role, f.Type))
	}
	return errs
}

// Field returns the declared field with the given name.
func (s Schema) Field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// DisplayName is the item's value for the field marked display, or "" when
// no field is marked or the item lacks it.
func (s Schema) DisplayName(facts map[string]Value) string {
	for _, f := range s.Fields {
		if f.Display {
			return facts[f.Name].Category
		}
	}
	return ""
}

// EffectiveWeight is the declared weight, or 1 when none is declared.
func (f Field) EffectiveWeight() float64 {
	if f.Weight == 0 {
		return 1
	}
	return f.Weight
}

// EffectiveMissing is the declared missing policy, or Keep.
func (f Field) EffectiveMissing() Missing {
	if f.Missing == "" {
		return Keep
	}
	return f.Missing
}

// IsFilter reports whether the field's role includes filtering.
func (f Field) IsFilter() bool { return f.Role == Filter || f.Role == Both }

// IsPreference reports whether the field's role includes preference.
func (f Field) IsPreference() bool { return f.Role == Preference || f.Role == Both }
