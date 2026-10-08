// Package check scores acquisition-list items against a member's taste and
// finds the shelf items most like each one (ADR 0002 section 10).
package check

import (
	"cmp"
	"math"
	"slices"

	"github.com/yaad-index/taste-machine/schema"
	"github.com/yaad-index/taste-machine/score"
)

// Closest is how many of the most similar shelf items a report lists.
const Closest = 3

// Neighbour is a shelf item and its similarity to the checked item.
type Neighbour struct {
	ID         string
	Similarity float64
}

// Report is the outcome for one acquisition-list item.
type Report struct {
	ID string
	// Excluded is set when the item was not scored: it is on the blocked
	// list, or a hard filter removes it.
	Excluded *score.Cause
	// Result is the item's score and explanation, when not excluded.
	Result score.Result
	// AlreadyInCatalogue marks an item the shelf already has.
	AlreadyInCatalogue bool
	// LikeThis counts the shelf items at or above the similarity
	// threshold; Closest lists up to three of them, closest first.
	LikeThis int
	Closest  []Neighbour
}

// Run checks every acquisition-list item. Scored items come first, ranked
// as results are; excluded items follow in id order.
func Run(mo *score.Model, threshold float64) []Report {
	items := mo.AcquisitionItems()
	passed, excluded := mo.Filters(items)
	shelf := mo.ShelfItems()
	passedFilters := mo.PassedFilters()

	var scored []score.Result
	for _, it := range passed {
		r := mo.Score(it)
		r.Passed = passedFilters
		scored = append(scored, r)
	}
	score.Rank(scored)

	byID := make(map[string]score.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	var out []Report
	for _, r := range scored {
		out = append(out, report(mo, byID[r.ID], shelf, threshold, Report{ID: r.ID, Result: r}))
	}
	for _, e := range excluded {
		c := e.Cause
		out = append(out, report(mo, byID[e.ID], shelf, threshold, Report{ID: e.ID, Excluded: &c}))
	}
	return out
}

func report(mo *score.Model, it score.Item, shelf []score.Item, threshold float64, r Report) Report {
	var near []Neighbour
	for _, other := range shelf {
		if other.ID == it.ID {
			r.AlreadyInCatalogue = true
			continue
		}
		if s := Similarity(mo, it, other); s >= threshold {
			near = append(near, Neighbour{ID: other.ID, Similarity: s})
		}
	}
	slices.SortFunc(near, func(a, b Neighbour) int {
		return cmp.Or(cmp.Compare(b.Similarity, a.Similarity), cmp.Compare(a.ID, b.ID))
	})
	r.LikeThis = len(near)
	r.Closest = near[:min(Closest, len(near))]
	return r
}

// Similarity is ADR 0002 section 10: over the catalogue fields whose role
// is preference or both and that either item has, Σ weight × field
// similarity / Σ weight. A field only one item has scores 0. Per-user
// fields are left out: they describe a person's history, not the item.
func Similarity(mo *score.Model, a, b score.Item) float64 {
	var sum, weights float64
	for _, f := range mo.CatalogueFields() {
		if !f.IsPreference() {
			continue
		}
		va, okA := a.Facts[f.Name]
		vb, okB := b.Facts[f.Name]
		if !okA && !okB {
			continue
		}
		w := f.EffectiveWeight()
		weights += w
		if okA && okB {
			sum += w * fieldSimilarity(mo, f, va, vb)
		}
	}
	if weights == 0 {
		return 0
	}
	return sum / weights
}

func fieldSimilarity(mo *score.Model, f schema.Field, a, b schema.Value) float64 {
	switch f.Type {
	case schema.Category, schema.Bool:
		if slices.Equal(a.Values(), b.Values()) {
			return 1
		}
		return 0
	case schema.Set:
		return jaccard(a.Set, b.Set)
	case schema.Number:
		e := mo.Edges[f.Name]
		return 1 - math.Abs(float64(e.Bucket(a.Number)-e.Bucket(b.Number)))/float64(e.Count())
	case schema.Votes:
		return 1 - totalVariation(a.Shares(), b.Shares())
	default:
		return 0
	}
}

// jaccard is |a ∩ b| / |a ∪ b|; two empty sets are identical.
func jaccard(a, b []string) float64 {
	union := map[string]bool{}
	for _, v := range a {
		union[v] = true
	}
	inter := 0
	for _, v := range b {
		if union[v] {
			inter++
		}
		union[v] = true
	}
	if len(union) == 0 {
		return 1
	}
	return float64(inter) / float64(len(union))
}

// totalVariation is half the summed absolute difference of two share
// distributions. A side with no votes is as far as can be from one with
// votes, and identical to another without.
func totalVariation(a, b map[string]float64) float64 {
	if a == nil || b == nil {
		if a == nil && b == nil {
			return 0
		}
		return 1
	}
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	slices.Sort(sorted)
	var d float64
	for _, k := range sorted {
		d += math.Abs(a[k] - b[k])
	}
	return d / 2
}
