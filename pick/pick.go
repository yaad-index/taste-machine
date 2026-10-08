// Package pick is the question flow of ADR 0002 sections 6 and 7: it asks
// the question that best narrows the remaining items, applies answers, and
// ranks what remains.
package pick

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
	"github.com/yaad-index/taste-machine/score"
)

// StopAt is the remaining size at which the flow stops asking.
const StopAt = 3

// Shown is how many options a question shows at a time.
const Shown = 3

// AnswerKind says what kind of answer was given.
type AnswerKind int

const (
	// Value picks one option.
	Value AnswerKind = iota
	// Other keeps the items with none of the shown options and asks the
	// same field again with the next options.
	Other
	// NoPreference skips the field.
	NoPreference
)

// Answer is one answer to a question.
type Answer struct {
	Field string
	Kind  AnswerKind
	// Key is the chosen option's key, for Value.
	Key string
	// Shown are the option keys the question showed, for Other.
	Shown []string
}

// Option is one possible answer.
type Option struct {
	Key   string
	Label string
	// Count is how many remaining items match it for narrowing.
	Count int
}

// Question asks about one field.
type Question struct {
	Field schema.Field
	// Options are the options shown, at most Shown of them.
	Options []Option
	// More is true when "other" leads to further options.
	More bool
}

// Outcome is what applying an answer did.
type Outcome struct {
	// Unmet is true for a preference answer no remaining item matched; it
	// gave way and remaining is unchanged.
	Unmet bool
	// Empty is set when a filter answer, or "other", left no items.
	Empty *score.EmptyReport
}

type applied struct {
	answer Answer
	field  schema.Field
	unmet  bool
	before []score.Item
	// done is true when the answer settled the field.
	done bool
}

// Session is one run of the question flow for one member.
type Session struct {
	mo        *score.Model
	remaining []score.Item
	excluded  []score.Exclusion
	answers   []applied
	// paging is the field an "other" answer is paging through.
	paging string
}

// New starts a session over the member's shelf, after the declared
// filters.
func New(mo *score.Model) *Session {
	passed, excluded := mo.Filters(mo.ShelfItems())
	return &Session{mo: mo, remaining: passed, excluded: excluded}
}

// Remaining is how many items are left.
func (s *Session) Remaining() int { return len(s.remaining) }

// Empty reports why the declared filters left nothing, or nil when items
// remain or the shelf was empty to begin with.
func (s *Session) Empty() *score.EmptyReport {
	if len(s.remaining) > 0 || len(s.excluded) == 0 || len(s.answers) > 0 {
		return nil
	}
	r := score.Report(s.excluded)
	return &r
}

// Next returns the next question, or false when the flow should stop:
// StopAt or fewer items remain, or no question splits them.
func (s *Session) Next() (Question, bool) {
	if len(s.remaining) <= StopAt {
		return Question{}, false
	}
	if s.paging != "" {
		f, _ := s.mo.Field(s.paging)
		if q, ok := s.question(f); ok {
			return q, true
		}
		s.paging = ""
	}
	var best *Question
	bestSize := math.Inf(1)
	for _, f := range s.mo.AllFields() {
		if !f.Askable || s.answered(f.Name) {
			continue
		}
		g := s.groups(f)
		if g.nonEmpty() < 2 {
			continue
		}
		if size := g.expected(); size < bestSize {
			q, ok := s.question(f)
			if !ok {
				continue
			}
			best, bestSize = &q, size
		}
	}
	if best == nil {
		return Question{}, false
	}
	return *best, true
}

// question builds the question for a field: its first Shown options by the
// member's affinity, then count, then key.
func (s *Session) question(f schema.Field) (Question, bool) {
	g := s.groups(f)
	if g.nonEmpty() < 2 {
		return Question{}, false
	}
	// Options already shown in this field need no skipping: "other" removed
	// every item that had one.
	var opts []Option
	for _, key := range g.keys() {
		opts = append(opts, Option{Key: key, Label: s.label(f, key), Count: len(g.members[key])})
	}
	slices.SortFunc(opts, func(a, b Option) int {
		return cmp.Or(
			cmp.Compare(s.mo.Affinity(f.Name, b.Key), s.mo.Affinity(f.Name, a.Key)),
			cmp.Compare(b.Count, a.Count),
			compareKeys(f, a.Key, b.Key),
		)
	})
	q := Question{Field: f, Options: opts[:min(Shown, len(opts))], More: len(opts) > Shown}
	return q, true
}

// Apply applies an answer (ADR 0002 section 6).
func (s *Session) Apply(a Answer) (Outcome, error) {
	f, ok := s.mo.Field(a.Field)
	if !ok {
		return Outcome{}, fmt.Errorf("unknown field %q", a.Field)
	}
	ap := applied{answer: a, field: f, before: s.remaining, done: true}
	var out Outcome
	switch a.Kind {
	case NoPreference:
	case Other:
		ap.done = false
		s.remaining = s.keep(f, func(it score.Item) bool {
			for _, k := range a.Shown {
				if matchesNarrow(s.mo, it, f, k) {
					return false
				}
			}
			return true
		})
		if len(s.remaining) == 0 {
			labels := make([]string, 0, len(a.Shown))
			for _, k := range a.Shown {
				labels = append(labels, s.label(f, k))
			}
			r := score.Report(answerExclusions(ap.before, f, "none of "+strings.Join(labels, ", ")))
			out.Empty = &r
		}
	case Value:
		match := func(it score.Item) bool { return matchesNarrow(s.mo, it, f, a.Key) }
		if f.Role == schema.Filter {
			s.remaining = s.keep(f, match)
			if len(s.remaining) == 0 {
				r := score.Report(answerExclusions(ap.before, f, s.label(f, a.Key)))
				out.Empty = &r
			}
		} else {
			if !slices.ContainsFunc(s.remaining, match) {
				ap.unmet = true
				out.Unmet = true
			} else {
				s.remaining = s.keep(f, match)
			}
		}
	default:
		return Outcome{}, fmt.Errorf("unknown answer kind %d", a.Kind)
	}
	s.answers = append(s.answers, ap)
	if a.Kind == Other {
		s.paging = f.Name
	} else if s.paging == f.Name {
		s.paging = ""
	}
	return out, nil
}

// Undo takes back the last answer and marks its field as skipped, so it is
// not asked again.
func (s *Session) Undo() {
	if len(s.answers) == 0 {
		return
	}
	last := s.answers[len(s.answers)-1]
	s.remaining = last.before
	s.answers[len(s.answers)-1] = applied{answer: Answer{Field: last.answer.Field, Kind: NoPreference}, field: last.field, before: last.before, done: true}
	if s.paging == last.answer.Field {
		s.paging = ""
	}
}

// keep returns the remaining items that match, plus those without the field
// when the field's missing policy keeps them.
func (s *Session) keep(f schema.Field, match func(score.Item) bool) []score.Item {
	var out []score.Item
	for _, it := range s.remaining {
		if !has(it, f) {
			if f.EffectiveMissing() == schema.Keep {
				out = append(out, it)
			}
			continue
		}
		if match(it) {
			out = append(out, it)
		}
	}
	return out
}

// answerExclusions blames an answer for removing items; answer is how the
// answer reads.
func answerExclusions(items []score.Item, f schema.Field, answer string) []score.Exclusion {
	out := make([]score.Exclusion, 0, len(items))
	for _, it := range items {
		out = append(out, score.Exclusion{ID: it.ID, Cause: score.Cause{Kind: score.ByAnswer, Field: f.Name, Value: answer}})
	}
	return out
}

func (s *Session) answered(field string) bool {
	return slices.ContainsFunc(s.answers, func(a applied) bool { return a.answer.Field == field && a.done })
}

// Results scores and ranks the remaining items: final score = taste score
// + B × the mean graded match over the preference answers given, met or
// unmet.
func (s *Session) Results() []score.Result {
	var prefs []applied
	for _, a := range s.answers {
		if a.answer.Kind == Value && a.field.Role != schema.Filter {
			prefs = append(prefs, a)
		}
	}
	passed := s.mo.PassedFilters()
	out := make([]score.Result, 0, len(s.remaining))
	for _, it := range s.remaining {
		r := s.mo.Score(it)
		r.Passed = passed
		if len(prefs) > 0 {
			var sum float64
			for _, a := range prefs {
				m := Match(s.mo, it, a.field, a.answer.Key)
				sum += m
				if m > 0 {
					r.Matched = append(r.Matched, fmt.Sprintf("%s = %s", a.field.Name, s.label(a.field, a.answer.Key)))
				}
			}
			r.Final = r.TasteScore + s.mo.Constants.AnswerWeight*sum/float64(len(prefs))
		}
		for _, a := range s.answers {
			if a.answer.Kind == Value && a.field.Role == schema.Filter && has(it, a.field) {
				r.Matched = append(r.Matched, fmt.Sprintf("%s = %s", a.field.Name, s.label(a.field, a.answer.Key)))
			}
		}
		out = append(out, r)
	}
	score.Rank(out)
	return out
}

// Match is the graded match of an item against a preference answer, used
// for scoring only: 1 or 0 for set, category and bool; 1 − |Δbucket| /
// buckets for number; the value's share for votes. An item without the
// field matches 0.
func Match(mo *score.Model, it score.Item, f schema.Field, key string) float64 {
	v, ok := it.Value(f.Name)
	if !ok {
		return 0
	}
	switch f.Type {
	case schema.Number:
		want, err := strconv.Atoi(key)
		if err != nil {
			return 0
		}
		e := mo.Edges[f.Name]
		return 1 - math.Abs(float64(e.Bucket(v.Number)-want))/float64(e.Count())
	case schema.Votes:
		return v.Shares()[key]
	case schema.Range:
		return 0
	default:
		if slices.Contains(v.Values(), key) {
			return 1
		}
		return 0
	}
}

// matchesNarrow is ADR 0002's "matches for narrowing": the item has the
// value; for number, the same bucket; for votes, the value has the item's
// top share (ties count); for range, the item fits N.
func matchesNarrow(mo *score.Model, it score.Item, f schema.Field, key string) bool {
	return slices.Contains(narrowKeys(mo, it, f), key)
}

// narrowKeys lists the option keys an item matches for narrowing.
func narrowKeys(mo *score.Model, it score.Item, f schema.Field) []string {
	v, ok := it.Value(f.Name)
	if !ok {
		return nil
	}
	switch f.Type {
	case schema.Number:
		return []string{strconv.Itoa(mo.Edges[f.Name].Bucket(v.Number))}
	case schema.Votes:
		shares := v.Shares()
		var top float64
		for _, sh := range shares {
			top = max(top, sh)
		}
		var out []string
		for k, sh := range shares {
			if sh == top && sh > 0 {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	case schema.Range:
		var out []string
		for n := math.Ceil(v.Range.Min); n <= math.Floor(v.Range.Max); n++ {
			out = append(out, strconv.FormatFloat(n, 'f', -1, 64))
		}
		return out
	default:
		return v.Values()
	}
}

// has reports whether an item holds anything for the field: an empty set or
// a votes field with no votes counts as missing.
func has(it score.Item, f schema.Field) bool {
	v, ok := it.Value(f.Name)
	if !ok {
		return false
	}
	switch f.Type {
	case schema.Set:
		return len(v.Set) > 0
	case schema.Votes:
		return v.Shares() != nil
	}
	return true
}

func (s *Session) label(f schema.Field, key string) string {
	if f.Type == schema.Range {
		return "fits " + key
	}
	return s.mo.ValueLabel(f, key)
}

// groups splits remaining by a field's options, plus a "none" group for
// items without the field.
type groups struct {
	f       schema.Field
	members map[string][]string
	none    int
}

func (s *Session) groups(f schema.Field) groups {
	g := groups{f: f, members: map[string][]string{}}
	for _, it := range s.remaining {
		if !has(it, f) {
			g.none++
			continue
		}
		for _, k := range narrowKeys(s.mo, it, f) {
			g.members[k] = append(g.members[k], it.ID)
		}
	}
	return g
}

func (g groups) nonEmpty() int {
	n := len(g.members)
	if g.none > 0 {
		n++
	}
	return n
}

// expected is the expected remaining size after an answer: Σ|g|² / G with
// G = Σ|g|, over every option and the "none" group.
func (g groups) expected() float64 {
	var sq, total float64
	for _, k := range g.keys() {
		n := float64(len(g.members[k]))
		sq += n * n
		total += n
	}
	n := float64(g.none)
	sq += n * n
	total += n
	if total == 0 {
		return 0
	}
	return sq / total
}

func (g groups) keys() []string {
	keys := make([]string, 0, len(g.members))
	for k := range g.members {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return compareKeys(g.f, a, b) })
	return keys
}

// compareKeys orders option keys: numerically for number buckets and range
// counts, otherwise as strings.
func compareKeys(f schema.Field, a, b string) int {
	if f.Type == schema.Number || f.Type == schema.Range {
		x, errA := strconv.ParseFloat(a, 64)
		y, errB := strconv.ParseFloat(b, 64)
		if errA == nil && errB == nil {
			return cmp.Compare(x, y)
		}
	}
	return cmp.Compare(a, b)
}

// StoredAnswer turns an answer stored in a taste file into an Answer: a
// JSON string names a value (category, bool, set, votes), a number gives a
// number field's value (bucketed with the shelf's edges) or a range
// field's N, and null means no preference.
func StoredAnswer(mo *score.Model, a fileformat.Answer) (Answer, error) {
	f, ok := mo.Field(a.Field)
	if !ok {
		return Answer{}, fmt.Errorf("answer on %q: unknown field", a.Field)
	}
	raw := a.Value
	if len(raw) == 0 || string(raw) == "null" {
		return Answer{Field: f.Name, Kind: NoPreference}, nil
	}
	switch f.Type {
	case schema.Number, schema.Range:
		var n float64
		if err := json.Unmarshal(raw, &n); err != nil {
			return Answer{}, fmt.Errorf("answer on %q: want a number: %w", f.Name, err)
		}
		if f.Type == schema.Number {
			return Answer{Field: f.Name, Key: strconv.Itoa(mo.Edges[f.Name].Bucket(n))}, nil
		}
		if n != math.Trunc(n) {
			return Answer{}, fmt.Errorf("answer on %q: want a whole number", f.Name)
		}
		return Answer{Field: f.Name, Key: strconv.FormatFloat(n, 'f', -1, 64)}, nil
	default:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return Answer{}, fmt.Errorf("answer on %q: want a string: %w", f.Name, err)
		}
		if f.Type == schema.Bool && v != "true" && v != "false" {
			return Answer{}, fmt.Errorf("answer on %q: want \"true\" or \"false\"", f.Name)
		}
		return Answer{Field: f.Name, Key: v}, nil
	}
}
