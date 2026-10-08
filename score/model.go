package score

import (
	"cmp"
	"math"
	"slices"
	"strconv"

	"github.com/yaad-index/taste-machine/dataset"
	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
)

// Item is one item as a member sees it: its facts and what their taste file
// says about it.
type Item struct {
	ID    string
	Facts map[string]schema.Value
	// Entry is nil when the taste file does not mention the item.
	Entry *fileformat.TasteItem
}

// Value returns the item's value for a catalogue or per-user field.
func (it Item) Value(field string) (schema.Value, bool) {
	if v, ok := it.Facts[field]; ok {
		return v, true
	}
	if it.Entry != nil {
		v, ok := it.Entry.Fields[field]
		return v, ok
	}
	return schema.Value{}, false
}

// pair is one value an item holds for a field, with its weight inside the
// field: 1 for each category, bool, set member or bucket, and the vote
// share for votes.
type pair struct {
	key    string
	weight float64
}

// Model is what one member's history says: the affinity of every value of
// every scored field, learned from their rated and played items.
type Model struct {
	Member    *dataset.Member
	Constants Constants
	// Fields are the scored fields, catalogue fields first, each in schema
	// order.
	Fields []schema.Field
	// Edges holds the bucket edges of every number field.
	Edges map[string]Edges
	// Mean is the member's mean rating, with direction applied.
	Mean  float64
	Scale fileformat.Scale

	affinity map[string]map[string]float64
	d        *dataset.Dataset
}

// Learn builds a member's model. Affinity follows ADR 0002 section 3:
// affinity(v) = Σ signal / (n + k), over the member's history items that
// have v. The history is the taste entries with a rating or at least one
// play: an entry with neither (an owned, untouched item) says nothing about
// taste and is not counted in n. A single play does count, with signal 0.
// For votes each item adds signal × share(v) to the sum and share(v) to n.
// A stated like or dislike replaces the learned affinity with +1 or -1.
func Learn(d *dataset.Dataset, m *dataset.Member) *Model {
	mo := &Model{
		Member:    m,
		Constants: For(m),
		Edges:     map[string]Edges{},
		Scale:     m.Meta.EffectiveScale(),
		affinity:  map[string]map[string]float64{},
		d:         d,
	}
	for _, f := range slices.Concat(d.Schema.Fields, m.Schema.Fields) {
		if f.Type == schema.Number {
			mo.Edges[f.Name] = edgesFor(f, mo.shelfValues(f.Name))
		}
		if f.IsPreference() {
			mo.Fields = append(mo.Fields, f)
		}
	}
	mo.Mean = mo.meanRating()

	sums := map[string]map[string]float64{}
	counts := map[string]map[string]float64{}
	for _, id := range m.IDs() {
		e := m.Entries[id]
		if e.Rating == nil && e.Plays == 0 {
			continue
		}
		s := mo.Signal(e)
		it := mo.Item(id)
		for _, f := range mo.Fields {
			for _, p := range mo.pairs(it, f) {
				add(sums, f.Name, p.key, s*p.weight)
				add(counts, f.Name, p.key, p.weight)
			}
		}
	}
	for _, field := range sortedKeys(sums) {
		for _, key := range sortedKeys(sums[field]) {
			setAffinity(mo.affinity, field, key, sums[field][key]/(counts[field][key]+mo.Constants.K))
		}
	}
	for _, field := range sortedKeys(m.Meta.Preferences) {
		for _, value := range sortedKeys(m.Meta.Preferences[field]) {
			switch m.Meta.Preferences[field][value] {
			case fileformat.Like:
				setAffinity(mo.affinity, field, value, 1)
			case fileformat.Dislike:
				setAffinity(mo.affinity, field, value, -1)
			}
		}
	}
	return mo
}

// Affinity is the member's affinity for one value of one field; a value
// with no history is 0.
func (mo *Model) Affinity(field, key string) float64 {
	return mo.affinity[field][key]
}

// Item returns the member's view of an item: the shelf's facts when the
// shelf has it, otherwise the learn-from catalogue's.
func (mo *Model) Item(id string) Item {
	facts, _ := mo.d.Facts(mo.Member, id)
	it := Item{ID: id, Facts: facts}
	if e, ok := mo.Member.Entries[id]; ok {
		it.Entry = &e
	}
	return it
}

// Rating returns the member's rating of an item with direction applied, so
// a higher number is always better.
func (mo *Model) Rating(e *fileformat.TasteItem) (float64, bool) {
	if e == nil || e.Rating == nil {
		return 0, false
	}
	r := *e.Rating
	if mo.Scale.Direction == fileformat.LowerBetter {
		r = mo.Scale.Min + mo.Scale.Max - r
	}
	return r, true
}

// Signal is ADR 0002 section 2: how much one history item says the member
// likes it, in [-1, 1].
func (mo *Model) Signal(e fileformat.TasteItem) float64 {
	if r, ok := mo.Rating(&e); ok {
		lo, hi, m := mo.Scale.Min, mo.Scale.Max, mo.Mean
		if r >= m {
			if hi-m == 0 {
				return 0
			}
			return (r - m) / (hi - m)
		}
		// r < m, so m > lo: this denominator cannot be zero.
		return (r - m) / (m - lo)
	}
	if e.Plays <= 1 {
		return 0
	}
	c := mo.Constants
	return c.PlayWeight * math.Min(1, math.Log2(float64(e.Plays))/math.Log2(c.PlaySaturation))
}

func (mo *Model) meanRating() float64 {
	var sum float64
	n := 0
	for _, id := range mo.Member.IDs() {
		e := mo.Member.Entries[id]
		if r, ok := mo.Rating(&e); ok {
			sum += r
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// shelfValues collects a number field's values over the shelf: facts for a
// catalogue field, the member's entries for a per-user field.
func (mo *Model) shelfValues(field string) []float64 {
	var out []float64
	for _, it := range mo.d.Shelf {
		v, ok := it.Facts[field]
		if !ok {
			if e, has := mo.Member.Entries[it.ID]; has {
				v, ok = e.Fields[field]
			}
		}
		if ok && v.Type == schema.Number {
			out = append(out, v.Number)
		}
	}
	return out
}

// pairs lists the values an item holds for a field, with their weight
// inside the field. A field the item lacks, or one with nothing in it,
// gives no pairs.
func (mo *Model) pairs(it Item, f schema.Field) []pair {
	v, ok := it.Value(f.Name)
	if !ok {
		return nil
	}
	switch f.Type {
	case schema.Number:
		return []pair{{key: strconv.Itoa(mo.Edges[f.Name].Bucket(v.Number)), weight: 1}}
	case schema.Votes:
		shares := v.Shares()
		out := make([]pair, 0, len(shares))
		for _, k := range sortedKeys(shares) {
			// A value with no votes is not held: with k = 0 it would give
			// an affinity of 0 / 0.
			if shares[k] > 0 {
				out = append(out, pair{key: k, weight: shares[k]})
			}
		}
		return out
	default:
		vals := v.Values()
		out := make([]pair, 0, len(vals))
		for _, k := range vals {
			out = append(out, pair{key: k, weight: 1})
		}
		return out
	}
}

// valueLabel is how a value key reads in an explanation: the value itself,
// or the bounds of a bucket.
func (mo *Model) valueLabel(f schema.Field, key string) string {
	if f.Type != schema.Number {
		return key
	}
	b, err := strconv.Atoi(key)
	if err != nil {
		return key
	}
	return mo.Edges[f.Name].Label(b)
}

func add(m map[string]map[string]float64, field, key string, v float64) {
	if m[field] == nil {
		m[field] = map[string]float64{}
	}
	m[field][key] += v
}

func setAffinity(m map[string]map[string]float64, field, key string, v float64) {
	if m[field] == nil {
		m[field] = map[string]float64{}
	}
	m[field][key] = v
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, cmp.Compare[string])
	return keys
}
