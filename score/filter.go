package score

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
)

// CauseKind says what excluded an item.
type CauseKind string

const (
	// ByFilter: a declared filter, such as a field's "never" value or the
	// missing policy of a filtered field.
	ByFilter CauseKind = "filter"
	// ByList: the member's blocked list.
	ByList CauseKind = "item list"
)

// Cause is why one item was excluded.
type Cause struct {
	Kind CauseKind
	// Field and Value name the filter; both are empty for ByList, and
	// Value is empty when the item lacked the field.
	Field string
	Value string
}

func (c Cause) String() string {
	switch {
	case c.Kind == ByList:
		return "on the blocked list"
	case c.Value == "":
		return fmt.Sprintf("no %s, and the filter on it drops items without it", c.Field)
	default:
		return fmt.Sprintf("%s is %s, marked never", c.Field, c.Value)
	}
}

// Exclusion is one item a filter removed.
type Exclusion struct {
	ID    string
	Cause Cause
}

// filter is one active hard filter of a member.
type filter struct {
	field schema.Field
	never map[string]bool
}

// Filters applies the member's hard filters to items, through one path
// (ADR 0002 sections 3, 4, 5 and 5b): the blocked list by id, then every
// field with a "never" value, including the missing policy of that field.
// Items keep their order. An excluded item gets the first cause found.
func (mo *Model) Filters(items []Item) (passed []Item, excluded []Exclusion) {
	filters := mo.activeFilters()
	for _, it := range items {
		if c, ok := mo.exclude(it, filters); ok {
			excluded = append(excluded, Exclusion{ID: it.ID, Cause: c})
			continue
		}
		passed = append(passed, it)
	}
	return passed, excluded
}

// PassedFilters describes the filters an item passed, for its explanation.
func (mo *Model) PassedFilters() []string {
	var out []string
	if len(mo.Member.Blocked) > 0 {
		out = append(out, "not on the blocked list")
	}
	for _, f := range mo.activeFilters() {
		out = append(out, fmt.Sprintf("%s is not %s", f.field.Name, joinSorted(f.never)))
	}
	return out
}

func (mo *Model) activeFilters() []filter {
	var out []filter
	for _, f := range slices.Concat(mo.d.Schema.Fields, mo.Member.Schema.Fields) {
		if !f.IsFilter() {
			continue
		}
		never := map[string]bool{}
		for value, stance := range mo.Member.Meta.Preferences[f.Name] {
			if stance == fileformat.Never {
				never[value] = true
			}
		}
		if len(never) > 0 {
			out = append(out, filter{field: f, never: never})
		}
	}
	return out
}

func (mo *Model) exclude(it Item, filters []filter) (Cause, bool) {
	if mo.Member.Blocked[it.ID] {
		return Cause{Kind: ByList}, true
	}
	for _, f := range filters {
		v, ok := it.Value(f.field.Name)
		if !ok {
			if f.field.EffectiveMissing() == schema.Drop {
				return Cause{Kind: ByFilter, Field: f.field.Name}, true
			}
			continue
		}
		for _, val := range v.Values() {
			if f.never[val] {
				return Cause{Kind: ByFilter, Field: f.field.Name, Value: val}, true
			}
		}
	}
	return Cause{}, false
}

// EmptyReport explains an empty result: how many items each cause removed,
// causes in a stable order.
type EmptyReport struct {
	Causes []CauseCount
}

// CauseCount is one cause and how many items it removed.
type CauseCount struct {
	Cause Cause
	Items int
}

// Report counts exclusions per cause.
func Report(excluded []Exclusion) EmptyReport {
	counts := map[Cause]int{}
	for _, e := range excluded {
		counts[e.Cause]++
	}
	var r EmptyReport
	for c, n := range counts {
		r.Causes = append(r.Causes, CauseCount{Cause: c, Items: n})
	}
	slices.SortFunc(r.Causes, func(a, b CauseCount) int {
		return cmp.Or(cmp.Compare(a.Cause.Kind, b.Cause.Kind), cmp.Compare(a.Cause.Field, b.Cause.Field), cmp.Compare(a.Cause.Value, b.Cause.Value))
	})
	return r
}

func joinSorted(set map[string]bool) string {
	return strings.Join(sortedKeys(set), ", ")
}
