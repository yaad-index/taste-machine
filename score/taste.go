package score

import "github.com/yaad-index/taste-machine/schema"

// Taste is what the question flow and check need from a taste: one
// member's Model, or a group of members.
type Taste interface {
	// ShelfItems and AcquisitionItems list items in id order.
	ShelfItems() []Item
	AcquisitionItems() []Item
	// Filters applies every hard filter, keeping the items' order.
	Filters(items []Item) (passed []Item, excluded []Exclusion)
	// PassedFilters describes the active filters, for explanations.
	PassedFilters() []string
	// Field looks up a field a question or an answer may name.
	Field(name string) (schema.Field, bool)
	// QuestionFields lists the fields questions may ask about, in order.
	QuestionFields() []schema.Field
	// CatalogueFields lists the catalogue's fields in schema order.
	CatalogueFields() []schema.Field
	// Affinity is the taste's affinity for one value of one field.
	Affinity(field, key string) float64
	// BucketEdges are a number field's bucket edges.
	BucketEdges(field string) Edges
	// ValueLabel is how a value key reads to a person.
	ValueLabel(f schema.Field, key string) string
	// Score scores one item.
	Score(it Item) Result
	// AnswerWeight is B, the weight of answers against the taste score.
	AnswerWeight() float64
}

var _ Taste = (*Model)(nil)

// QuestionFields lists the catalogue fields, then the member's per-user
// fields.
func (mo *Model) QuestionFields() []schema.Field { return mo.AllFields() }

// BucketEdges are a number field's bucket edges.
func (mo *Model) BucketEdges(field string) Edges { return mo.Edges[field] }

// AnswerWeight is the member's B.
func (mo *Model) AnswerWeight() float64 { return mo.Constants.AnswerWeight }

// View returns the item as this member sees it: its facts with the
// member's taste entry, if any.
func (mo *Model) View(it Item) Item {
	v := Item{ID: it.ID, Facts: it.Facts}
	if e, ok := mo.Member.Entries[it.ID]; ok {
		v.Entry = &e
	}
	return v
}
