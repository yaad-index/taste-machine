// Package group is group mode (ADR 0003): one shelf scored for several
// members at once, as one taste the question flow and check can use.
package group

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/yaad-index/taste-machine/dataset"
	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
	"github.com/yaad-index/taste-machine/score"
)

// Group is several members' tastes over one shelf. It implements
// score.Taste.
type Group struct {
	// Members are the members' models, in label order.
	Members []*score.Model
	// Size is what the group-size field is checked against.
	Size float64

	answerWeight float64
	d            *dataset.Dataset
	sizeField    *schema.Field
}

var _ score.Taste = (*Group)(nil)

// New builds a group from a dataset with at least two members. size is the
// group size the group-size field is checked against; 0 means the number of
// members. answerWeight is B; the members' own B values are ignored.
func New(d *dataset.Dataset, size int, answerWeight float64) (*Group, error) {
	if len(d.Members) < 2 {
		return nil, fmt.Errorf("group mode needs at least 2 taste files, got %d; one person uses single-user mode", len(d.Members))
	}
	if size < 0 {
		return nil, errors.New("negative group size")
	}
	if answerWeight < 0 {
		return nil, errors.New("negative answer weight")
	}
	if size == 0 {
		size = len(d.Members)
	}
	g := &Group{Size: float64(size), answerWeight: answerWeight, d: d}
	for _, m := range d.Members {
		g.Members = append(g.Members, score.Learn(d, m))
	}
	for _, f := range d.Schema.Fields {
		if f.GroupSize {
			g.sizeField = &f
		}
	}
	return g, nil
}

func items(src []fileformat.Item) []score.Item {
	out := make([]score.Item, 0, len(src))
	for _, it := range src {
		out = append(out, score.Item{ID: it.ID, Facts: it.Facts})
	}
	return out
}

// ShelfItems lists the shelf's items in id order.
func (g *Group) ShelfItems() []score.Item { return items(g.d.Shelf) }

// AcquisitionItems lists the acquisition list's items in id order.
func (g *Group) AcquisitionItems() []score.Item { return items(g.d.Acquisition) }

// Filters applies ADR 0003 section 3: the group-size field against Size,
// then every member's own hard filters and blocked list, in label order. An
// item any member excludes is excluded, the cause naming the member.
func (g *Group) Filters(in []score.Item) (passed []score.Item, excluded []score.Exclusion) {
	for _, it := range in {
		if c, ok := g.exclude(it); ok {
			excluded = append(excluded, score.Exclusion{ID: it.ID, Cause: c})
			continue
		}
		passed = append(passed, it)
	}
	return passed, excluded
}

func (g *Group) exclude(it score.Item) (score.Cause, bool) {
	if f := g.sizeField; f != nil {
		v, ok := it.Facts[f.Name]
		switch {
		case !ok && f.EffectiveMissing() == schema.Drop:
			return score.Cause{Kind: score.ByGroupSize, Field: f.Name}, true
		case ok && !v.Range.Contains(g.Size):
			return score.Cause{Kind: score.ByGroupSize, Field: f.Name, Value: g.sizeLabel()}, true
		}
	}
	for _, mo := range g.Members {
		_, ex := mo.Filters([]score.Item{mo.View(it)})
		if len(ex) > 0 {
			c := ex[0].Cause
			c.Member = mo.Member.Label
			return c, true
		}
	}
	return score.Cause{}, false
}

func (g *Group) sizeLabel() string { return strconv.FormatFloat(g.Size, 'f', -1, 64) }

// PassedFilters describes the group-size check and each member's filters.
func (g *Group) PassedFilters() []string {
	var out []string
	if g.sizeField != nil {
		out = append(out, fmt.Sprintf("%s fits %s, the group size", g.sizeField.Name, g.sizeLabel()))
	}
	for _, mo := range g.Members {
		for _, p := range mo.PassedFilters() {
			out = append(out, mo.Member.Label+": "+p)
		}
	}
	return out
}

// Field looks up a catalogue field.
func (g *Group) Field(name string) (schema.Field, bool) { return g.d.Schema.Field(name) }

// QuestionFields lists the catalogue fields a group question may ask about.
// The group-size field is set from the group, not asked, and per-user
// fields have a different value for each member, so neither is asked.
func (g *Group) QuestionFields() []schema.Field {
	var out []schema.Field
	for _, f := range g.d.Schema.Fields {
		if !f.GroupSize {
			out = append(out, f)
		}
	}
	return out
}

// CatalogueFields lists the catalogue's fields in schema order.
func (g *Group) CatalogueFields() []schema.Field { return g.d.Schema.Fields }

// Affinity is the mean of the members' affinities (ADR 0003 section 5).
func (g *Group) Affinity(field, key string) float64 {
	var sum float64
	for _, mo := range g.Members {
		sum += mo.Affinity(field, key)
	}
	return sum / float64(len(g.Members))
}

// BucketEdges are a number field's edges. They come from the shelf, so
// every member has the same ones.
func (g *Group) BucketEdges(field string) score.Edges { return g.Members[0].BucketEdges(field) }

// ValueLabel is how a value key reads to a person.
func (g *Group) ValueLabel(f schema.Field, key string) string {
	return g.Members[0].ValueLabel(f, key)
}

// AnswerWeight is the group's B.
func (g *Group) AnswerWeight() float64 { return g.answerWeight }

// Contributions shown per member (ADR 0003 section 6).
const (
	memberPositive = 2
	memberNegative = 1
)

// Score is ADR 0003 section 4: the group taste score is the mean of the
// members' taste scores, a favourite counting 1 for that member. Floor is
// the lowest member score, for the tie-break; Rating is the mean rating
// over the members who rated the item, each put on [0, 1] on that member's
// own scale so members with different scales weigh the same.
func (g *Group) Score(it score.Item) score.Result {
	res := score.Result{ID: it.ID}
	var sum, ratings float64
	rated := 0
	for i, mo := range g.Members {
		r := mo.Score(mo.View(it))
		sum += r.TasteScore
		if i == 0 || r.TasteScore < res.Floor {
			res.Floor = r.TasteScore
		}
		if r.HasRated {
			ratings += (r.Rating - mo.Scale.Min) / (mo.Scale.Max - mo.Scale.Min)
			rated++
		}
		res.Members = append(res.Members, score.MemberScore{
			Label:     mo.Member.Label,
			Score:     r.TasteScore,
			Favourite: r.Favourite,
			Positive:  r.Positive(memberPositive),
			Negative:  r.Negative(memberNegative),
		})
	}
	res.TasteScore = sum / float64(len(g.Members))
	res.Computed = res.TasteScore
	res.Final = res.TasteScore
	if rated > 0 {
		res.Rating = ratings / float64(rated)
		res.HasRated = true
	}
	return res
}
