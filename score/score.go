package score

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/yaad-index/taste-machine/schema"
)

// Scorer scores one item for one member. ADR 0001 keeps v1 matching behind
// this interface so a later ADR can add other matchers.
type Scorer interface {
	Score(it Item) Result
}

// Contribution is one value's share of a taste score.
type Contribution struct {
	Field  string
	Value  string
	Amount float64
}

// Result is a scored item with its explanation.
type Result struct {
	ID string
	// TasteScore is in [-1, 1]: 1 for a favourite, otherwise Computed.
	TasteScore float64
	// Computed is the score the history gives, before the favourites list.
	Computed float64
	// Final is what results are ranked by. Scoring sets it to TasteScore;
	// the question flow adds the answers.
	Final float64
	// Favourite marks an item on the member's favourites list.
	Favourite bool
	// Contributions sum to Computed, largest first.
	Contributions []Contribution
	// Passed lists the filters the item passed.
	Passed []string
	// Matched lists the answers the item matched, set by the question flow.
	Matched []string
	// Rating is the member's rating with direction applied; in group mode,
	// the mean over the members who rated the item.
	Rating   float64
	HasRated bool
	// Members holds each member's part in group mode, in label order.
	Members []MemberScore
	// Floor is the lowest member taste score in group mode.
	Floor float64
}

// MemberScore is one member's part of a group result.
type MemberScore struct {
	Label     string
	Score     float64
	Favourite bool
	// Positive and Negative are the member's top contributions.
	Positive []Contribution
	Negative []Contribution
}

// Positive returns the n largest positive contributions.
func (r Result) Positive(n int) []Contribution {
	var out []Contribution
	for _, c := range r.Contributions {
		if c.Amount > 0 && len(out) < n {
			out = append(out, c)
		}
	}
	return out
}

// Negative returns the n most negative contributions, most negative first.
func (r Result) Negative(n int) []Contribution {
	var out []Contribution
	for i := len(r.Contributions) - 1; i >= 0 && len(out) < n; i-- {
		if c := r.Contributions[i]; c.Amount < 0 {
			out = append(out, c)
		}
	}
	return out
}

// Score implements Scorer: ADR 0002 section 4. The field score is the
// weighted mean of the affinities of the item's values for the field (vote
// shares as weights, unknown values 0); the taste score is the weighted mean
// of the field scores over every preference field, a missing field counting
// as 0.
func (mo *Model) Score(it Item) Result {
	res := Result{ID: it.ID}
	var total float64
	for _, f := range mo.Fields {
		total += f.EffectiveWeight()
	}
	if total > 0 {
		for _, f := range mo.Fields {
			ps := mo.pairs(it, f)
			var inField float64
			for _, p := range ps {
				inField += p.weight
			}
			for _, p := range ps {
				amount := f.EffectiveWeight() / total * p.weight / inField * mo.Affinity(f.Name, p.key)
				res.Computed += amount
				if amount != 0 {
					res.Contributions = append(res.Contributions, Contribution{Field: f.Name, Value: mo.ValueLabel(f, p.key), Amount: amount})
				}
			}
		}
	}
	slices.SortStableFunc(res.Contributions, func(a, b Contribution) int {
		return cmp.Or(cmp.Compare(b.Amount, a.Amount), cmp.Compare(a.Field, b.Field), cmp.Compare(a.Value, b.Value))
	})
	res.TasteScore = res.Computed
	if mo.Member.Favourites[it.ID] {
		res.Favourite = true
		res.TasteScore = 1
	}
	res.Final = res.TasteScore
	res.Rating, res.HasRated = mo.Rating(it.Entry)
	return res
}

// Rank sorts results by ADR 0002 section 8: final score descending, then
// the member's rating descending with unrated items last, then id. Group
// results break a tie on the final score by the lowest member score first
// (ADR 0003 section 4).
func Rank(results []Result) {
	slices.SortFunc(results, func(a, b Result) int {
		if c := cmp.Compare(b.Final, a.Final); c != 0 {
			return c
		}
		// ADR 0003 section 4: in a group, the item that leaves nobody
		// worst off goes first.
		if len(a.Members) > 0 && len(b.Members) > 0 {
			if c := cmp.Compare(b.Floor, a.Floor); c != 0 {
				return c
			}
		}
		if a.HasRated != b.HasRated {
			if a.HasRated {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(b.Rating, a.Rating), cmp.Compare(a.ID, b.ID))
	})
}

// Outcome is a member's scored shelf.
type Outcome struct {
	// Results are the items that passed the filters, ranked.
	Results []Result
	// Excluded lists the items the filters removed, in id order.
	Excluded []Exclusion
	// Empty is set when filters removed every item.
	Empty *EmptyReport
}

// ShelfItems returns every shelf item as the member sees it, in id order.
func (mo *Model) ShelfItems() []Item {
	out := make([]Item, 0, len(mo.d.Shelf))
	for _, it := range mo.d.Shelf {
		out = append(out, mo.Item(it.ID))
	}
	return out
}

// ScoreAll filters, scores and ranks items.
func (mo *Model) ScoreAll(items []Item) Outcome {
	passed, excluded := mo.Filters(items)
	o := Outcome{Excluded: excluded}
	filters := mo.PassedFilters()
	for _, it := range passed {
		r := mo.Score(it)
		r.Passed = filters
		o.Results = append(o.Results, r)
	}
	Rank(o.Results)
	if len(passed) == 0 && len(excluded) > 0 {
		r := Report(excluded)
		o.Empty = &r
	}
	return o
}

// Explain renders a result's explanation (ADR 0002 section 9): its top 3
// positive and top 2 negative contributions, the answers it matched, the
// filters it passed, and whether it is on the favourites list.
func (r Result) Explain() string {
	if len(r.Members) > 0 {
		return r.explainGroup()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  score %.3f", r.ID, r.Final)
	if r.Favourite {
		fmt.Fprintf(&b, "  (favourite: score shown is 1, history gives %.3f)", r.Computed)
	}
	b.WriteString("\n")
	for _, c := range r.Positive(3) {
		fmt.Fprintf(&b, "  + %s = %s  %+.3f\n", c.Field, c.Value, c.Amount)
	}
	for _, c := range r.Negative(2) {
		fmt.Fprintf(&b, "  - %s = %s  %+.3f\n", c.Field, c.Value, c.Amount)
	}
	for _, m := range r.Matched {
		fmt.Fprintf(&b, "  matched: %s\n", m)
	}
	for _, p := range r.Passed {
		fmt.Fprintf(&b, "  passed: %s\n", p)
	}
	return b.String()
}

// AcquisitionItems returns every acquisition-list item as the member sees
// it, in id order. An item the taste file mentions carries its entry.
func (mo *Model) AcquisitionItems() []Item {
	out := make([]Item, 0, len(mo.d.Acquisition))
	for _, it := range mo.d.Acquisition {
		item := Item{ID: it.ID, Facts: it.Facts}
		if e, ok := mo.Member.Entries[it.ID]; ok {
			item.Entry = &e
		}
		out = append(out, item)
	}
	return out
}

// CatalogueFields lists the catalogue's fields in schema order.
func (mo *Model) CatalogueFields() []schema.Field { return mo.d.Schema.Fields }

// explainGroup renders a group result (ADR 0003 section 6): the group
// score, each member's score with their top 2 positive and top 1 negative
// contributions, the answers matched, the filters passed, and which members
// have the item as a favourite.
func (r Result) explainGroup() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  group score %.3f\n", r.ID, r.Final)
	var favs []string
	for _, m := range r.Members {
		fmt.Fprintf(&b, "  %s: %.3f", m.Label, m.Score)
		if m.Favourite {
			b.WriteString(" (favourite)")
			favs = append(favs, m.Label)
		}
		b.WriteString("\n")
		for _, c := range m.Positive {
			fmt.Fprintf(&b, "    + %s = %s  %+.3f\n", c.Field, c.Value, c.Amount)
		}
		for _, c := range m.Negative {
			fmt.Fprintf(&b, "    - %s = %s  %+.3f\n", c.Field, c.Value, c.Amount)
		}
	}
	for _, m := range r.Matched {
		fmt.Fprintf(&b, "  matched: %s\n", m)
	}
	for _, p := range r.Passed {
		fmt.Fprintf(&b, "  passed: %s\n", p)
	}
	if len(favs) > 0 {
		fmt.Fprintf(&b, "  favourite of: %s\n", strings.Join(favs, ", "))
	}
	return b.String()
}
