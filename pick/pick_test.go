package pick_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/dataset"
	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/pick"
	"github.com/yaad-index/taste-machine/schema"
	"github.com/yaad-index/taste-machine/score"
)

const eps = 1e-9

var (
	tagsF   = schema.Field{Name: "tags", Type: schema.Set, Role: schema.Both, Askable: true}
	themeF  = schema.Field{Name: "theme", Type: schema.Category, Role: schema.Preference, Askable: true}
	weightF = schema.Field{Name: "weight", Type: schema.Number, Role: schema.Preference, Askable: true, Edges: []float64{2, 3}}
	soloF   = schema.Field{Name: "solo", Type: schema.Bool, Role: schema.Filter, Askable: true}
	fitsF   = schema.Field{Name: "fits", Type: schema.Range, Role: schema.Filter, Askable: true}
)

func cat(v string) schema.Value { return schema.Value{Type: schema.Category, Category: v} }

func set(v ...string) schema.Value { return schema.Value{Type: schema.Set, Set: v} }

func num(v float64) schema.Value { return schema.Value{Type: schema.Number, Number: v} }

func boolean(v bool) schema.Value { return schema.Value{Type: schema.Bool, Bool: v} }

func fits(lo, hi float64) schema.Value {
	return schema.Value{Type: schema.Range, Range: schema.RangeValue{Min: lo, Max: hi}}
}

func r(v float64) *float64 { return &v }

// shelf builds eight items whose expected remaining sizes are, per field:
// tags (x on six, y on two) 5, theme (4 and 4) 4, weight (buckets of 2, 3
// and 3) 2.75, solo (1 and 7) 6.25, fits (every item fits 1 to 4) 8.
func shelf() []fileformat.Item {
	type row struct {
		id     string
		tag    string
		theme  string
		weight float64
		solo   bool
	}
	rows := []row{
		{"a", "x", "sea", 1, true},
		{"b", "x", "sea", 1, false},
		{"c", "x", "sea", 2.5, false},
		{"d", "x", "sea", 2.5, false},
		{"e", "x", "space", 2.5, false},
		{"f", "x", "space", 3.5, false},
		{"g", "y", "space", 3.5, false},
		{"h", "y", "space", 3.5, false},
	}
	var out []fileformat.Item
	for _, rw := range rows {
		out = append(out, fileformat.Item{ID: rw.id, Facts: map[string]schema.Value{
			"tags": set(rw.tag), "theme": cat(rw.theme), "weight": num(rw.weight), "solo": boolean(rw.solo), "fits": fits(1, 4),
		}})
	}
	return out
}

func model(t *testing.T, fields []schema.Field, items []fileformat.Item, meta *fileformat.TasteMeta, taste ...fileformat.TasteItem) *score.Model {
	t.Helper()
	sh := &fileformat.Catalogue{
		Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindCatalogue, Schema: schema.Schema{Fields: fields}},
		Items: items,
	}
	tf := &fileformat.Taste{
		Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindTaste, Taste: meta},
		Items: taste,
	}
	d, err := dataset.Load(sh, []dataset.Input{{Taste: tf, Name: "me"}}, nil)
	require.NoError(t, err)
	return score.Learn(d, d.Members[0])
}

func allFields() []schema.Field { return []schema.Field{tagsF, themeF, weightF, soloF, fitsF} }

func ids(items []score.Result) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func keys(q pick.Question) []string {
	var out []string
	for _, o := range q.Options {
		out = append(out, o.Key)
	}
	return out
}

func TestNextPicksLowestExpectedSize(t *testing.T) {
	s := pick.New(model(t, allFields(), shelf(), nil))
	q, ok := s.Next()
	require.True(t, ok)
	assert.Equal(t, "weight", q.Field.Name)
	assert.Equal(t, []pick.Option{
		{Key: "1", Label: "2 to < 3", Count: 3},
		{Key: "2", Label: ">= 3", Count: 3},
		{Key: "0", Label: "< 2", Count: 2},
	}, q.Options, "no history: options by count, then key")
	assert.False(t, q.More)

	_, err := s.Apply(pick.Answer{Field: "weight", Kind: pick.NoPreference})
	require.NoError(t, err)
	q, ok = s.Next()
	require.True(t, ok)
	assert.Equal(t, "theme", q.Field.Name, "an answered field is not asked again; theme (4) beats tags (5)")
}

func TestNextTieBreaksBySchemaOrder(t *testing.T) {
	other := schema.Field{Name: "mood", Type: schema.Category, Role: schema.Preference, Askable: true}
	items := shelf()
	for _, it := range items {
		it.Facts["mood"] = it.Facts["theme"]
	}
	s := pick.New(model(t, []schema.Field{other, themeF}, items, nil))
	q, ok := s.Next()
	require.True(t, ok)
	assert.Equal(t, "mood", q.Field.Name)
}

func TestNextSkipsUnaskableAndUnsplitting(t *testing.T) {
	quiet := themeF
	quiet.Askable = false
	s := pick.New(model(t, []schema.Field{quiet, fitsF}, shelf(), nil))
	q, ok := s.Next()
	require.True(t, ok, "fits splits into four groups, all of every item")
	assert.Equal(t, "fits", q.Field.Name)

	items := shelf()
	for _, it := range items {
		it.Facts["theme"] = cat("sea")
	}
	s = pick.New(model(t, []schema.Field{themeF}, items, nil))
	_, ok = s.Next()
	assert.False(t, ok, "a field with one value does not split")
}

func TestOptionsOrderedByAffinity(t *testing.T) {
	mo := model(t, allFields(), shelf(), nil, fileformat.TasteItem{ID: "a", Rating: r(10)}, fileformat.TasteItem{ID: "h", Rating: r(2)})
	s := pick.New(mo)
	q, _ := s.Next()
	require.Equal(t, "weight", q.Field.Name)
	assert.Equal(t, []string{"0", "1", "2"}, keys(q), "bucket 0 is liked, bucket 2 disliked, bucket 1 unknown")
}

func TestPreferenceAnswer(t *testing.T) {
	s := pick.New(model(t, allFields(), shelf(), nil))
	out, err := s.Apply(pick.Answer{Field: "theme", Key: "space"})
	require.NoError(t, err)
	assert.False(t, out.Unmet)
	assert.Equal(t, 4, s.Remaining())

	out, err = s.Apply(pick.Answer{Field: "tags", Key: "z"})
	require.NoError(t, err)
	assert.True(t, out.Unmet, "nothing has z: the answer gives way")
	assert.Equal(t, 4, s.Remaining())
	assert.Nil(t, out.Empty)
}

func TestFilterAnswerEmptiesAndUndo(t *testing.T) {
	items := shelf()[1:]
	s := pick.New(model(t, allFields(), items, nil))
	out, err := s.Apply(pick.Answer{Field: "solo", Key: "true"})
	require.NoError(t, err)
	assert.Equal(t, 0, s.Remaining())
	require.NotNil(t, out.Empty)
	assert.Equal(t, []score.CauseCount{{Cause: score.Cause{Kind: score.ByAnswer, Field: "solo", Value: "true"}, Items: 7}}, out.Empty.Causes)
	assert.Equal(t, "does not match the answer to solo (true)", out.Empty.Causes[0].Cause.String())

	s.Undo()
	assert.Equal(t, 7, s.Remaining())
	q, ok := s.Next()
	require.True(t, ok)
	assert.NotEqual(t, "solo", q.Field.Name, "an undone answer counts as no preference")
}

func TestFilterAnswerNarrowsHard(t *testing.T) {
	s := pick.New(model(t, allFields(), shelf(), nil))
	_, err := s.Apply(pick.Answer{Field: "solo", Key: "true"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, ids(s.Results()))
}

func TestRangeAnswer(t *testing.T) {
	items := shelf()
	items[0].Facts["fits"] = fits(1, 1)
	items[1].Facts["fits"] = fits(2.5, 6)
	s := pick.New(model(t, []schema.Field{fitsF}, items, nil))
	q, ok := s.Next()
	require.True(t, ok)
	assert.Equal(t, []pick.Option{
		{Key: "1", Label: "fits 1", Count: 7},
		{Key: "3", Label: "fits 3", Count: 7},
		{Key: "4", Label: "fits 4", Count: 7},
	}, q.Options, "range options: whole N within each item's range (b fits 3 to 6, not 2), by count then N")
	assert.True(t, q.More)
	_, err := s.Apply(pick.Answer{Field: "fits", Key: "5"})
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, ids(s.Results()))
}

func TestMissingPolicyAppliesToAnswers(t *testing.T) {
	items := shelf()
	delete(items[0].Facts, "theme")
	s := pick.New(model(t, allFields(), items, nil))
	_, err := s.Apply(pick.Answer{Field: "theme", Key: "space"})
	require.NoError(t, err)
	assert.Equal(t, 5, s.Remaining(), "keep: the item without theme survives")

	drop := themeF
	drop.Missing = schema.Drop
	s = pick.New(model(t, []schema.Field{tagsF, drop}, items, nil))
	_, err = s.Apply(pick.Answer{Field: "theme", Key: "space"})
	require.NoError(t, err)
	assert.Equal(t, 4, s.Remaining(), "drop: it does not")
}

func TestOtherPagesThroughOptions(t *testing.T) {
	var items []fileformat.Item
	for i, tag := range []string{"p", "p", "q", "q", "r", "r", "s", "t"} {
		items = append(items, fileformat.Item{ID: string(rune('a' + i)), Facts: map[string]schema.Value{"theme": cat(tag)}})
	}
	s := pick.New(model(t, []schema.Field{themeF}, items, nil))
	q, ok := s.Next()
	require.True(t, ok)
	assert.Equal(t, []string{"p", "q", "r"}, keys(q))
	assert.True(t, q.More)
	_, err := s.Apply(pick.Answer{Field: "theme", Kind: pick.Other, Shown: keys(q)})
	require.NoError(t, err)
	assert.Equal(t, 2, s.Remaining(), "other keeps the items with none of the shown values")
	_, ok = s.Next()
	assert.False(t, ok, "two left: the flow stops")

	items = append(items, fileformat.Item{ID: "x", Facts: map[string]schema.Value{"theme": cat("u")}}, fileformat.Item{ID: "y", Facts: map[string]schema.Value{"theme": cat("u")}})
	s = pick.New(model(t, []schema.Field{themeF}, items, nil))
	q, _ = s.Next()
	_, err = s.Apply(pick.Answer{Field: "theme", Kind: pick.Other, Shown: keys(q)})
	require.NoError(t, err)
	q, ok = s.Next()
	require.True(t, ok)
	assert.Equal(t, "theme", q.Field.Name, "other asks the same field again")
	assert.Equal(t, []string{"u", "s", "t"}, keys(q), "with the next options")
}

func TestVotesNarrowByTopShare(t *testing.T) {
	best := schema.Field{Name: "best", Type: schema.Votes, Role: schema.Preference, Askable: true}
	v := func(m map[string]float64) schema.Value { return schema.Value{Type: schema.Votes, Votes: m} }
	items := []fileformat.Item{
		{ID: "a", Facts: map[string]schema.Value{"best": v(map[string]float64{"2": 5, "3": 1})}},
		{ID: "b", Facts: map[string]schema.Value{"best": v(map[string]float64{"2": 2, "3": 2})}},
		{ID: "c", Facts: map[string]schema.Value{"best": v(map[string]float64{"2": 1, "3": 4})}},
		{ID: "d", Facts: map[string]schema.Value{"best": v(map[string]float64{"4": 1})}},
		{ID: "e", Facts: map[string]schema.Value{"best": v(map[string]float64{"4": 0})}},
	}
	mo := model(t, []schema.Field{best}, items, nil)
	s := pick.New(mo)
	_, err := s.Apply(pick.Answer{Field: "best", Key: "2"})
	require.NoError(t, err)
	res := s.Results()
	assert.Equal(t, []string{"a", "b", "e"}, ids(res), "a's top is 2; b ties 2 and 3; e has no votes and is kept as missing")
	assert.InDelta(t, 5.0/6, res[0].Final, eps, "graded match is the value's share")
	assert.InDelta(t, 0.5, res[1].Final, eps)
	assert.InDelta(t, 0.0, res[2].Final, eps)
}

func TestResultsAddAnswerMatch(t *testing.T) {
	mo := model(t, allFields(), shelf(), &fileformat.TasteMeta{Constants: fileformat.Constants{AnswerWeight: r(2)}})
	s := pick.New(mo)
	_, err := s.Apply(pick.Answer{Field: "weight", Key: "0"})
	require.NoError(t, err)
	_, err = s.Apply(pick.Answer{Field: "tags", Key: "z"})
	require.NoError(t, err)
	res := s.Results()
	require.Equal(t, []string{"a", "b"}, ids(res))
	// no history, so taste is 0; weight matches 1, the unmet z matches 0.
	assert.InDelta(t, 2*(1+0)/2.0, res[0].Final, eps)
	assert.Equal(t, []string{"weight = < 2"}, res[0].Matched)
	assert.Contains(t, res[0].Explain(), "matched: weight = < 2")
}

func TestNumberGradedMatch(t *testing.T) {
	mo := model(t, allFields(), shelf(), nil)
	items := mo.ShelfItems()
	assert.InDelta(t, 1.0, pick.Match(mo, items[0], weightF, "0"), eps)
	assert.InDelta(t, 1-1.0/3, pick.Match(mo, items[2], weightF, "0"), eps)
	assert.InDelta(t, 1-2.0/3, pick.Match(mo, items[7], weightF, "0"), eps)
	assert.InDelta(t, 0.0, pick.Match(mo, items[0], tagsF, "y"), eps)
	assert.InDelta(t, 1.0, pick.Match(mo, items[0], tagsF, "x"), eps)
	assert.InDelta(t, 0.0, pick.Match(mo, items[0], fitsF, "2"), eps, "a range field is never graded")
}

func TestStopsAtThree(t *testing.T) {
	s := pick.New(model(t, allFields(), shelf()[:3], nil))
	_, ok := s.Next()
	assert.False(t, ok)
}

func TestEmptyFromDeclaredFilters(t *testing.T) {
	s := pick.New(model(t, allFields(), shelf(), &fileformat.TasteMeta{Preferences: map[string]map[string]fileformat.Stance{"tags": {"x": fileformat.Never, "y": fileformat.Never}}}))
	require.NotNil(t, s.Empty())
	assert.Equal(t, 8, s.Empty().Causes[0].Items+s.Empty().Causes[1].Items)
	assert.Nil(t, pick.New(model(t, allFields(), shelf(), nil)).Empty())
}

func TestApplyErrors(t *testing.T) {
	s := pick.New(model(t, allFields(), shelf(), nil))
	_, err := s.Apply(pick.Answer{Field: "colour", Key: "red"})
	assert.ErrorContains(t, err, `unknown field "colour"`)
	_, err = s.Apply(pick.Answer{Field: "theme", Kind: pick.AnswerKind(9)})
	assert.ErrorContains(t, err, "unknown answer kind")
}

func TestStoredAnswer(t *testing.T) {
	mo := model(t, allFields(), shelf(), nil)
	cases := []struct {
		field, raw string
		want       pick.Answer
		err        string
	}{
		{"theme", `"sea"`, pick.Answer{Field: "theme", Key: "sea"}, ""},
		{"solo", `"true"`, pick.Answer{Field: "solo", Key: "true"}, ""},
		{"weight", `2.7`, pick.Answer{Field: "weight", Key: "1"}, ""},
		{"fits", `3`, pick.Answer{Field: "fits", Key: "3"}, ""},
		{"theme", `null`, pick.Answer{Field: "theme", Kind: pick.NoPreference}, ""},
		{"theme", ``, pick.Answer{Field: "theme", Kind: pick.NoPreference}, ""},
		{"fits", `2.5`, pick.Answer{}, "want a whole number"},
		{"weight", `"heavy"`, pick.Answer{}, "want a number"},
		{"theme", `3`, pick.Answer{}, "want a string"},
		{"solo", `"yes"`, pick.Answer{}, `want "true" or "false"`},
		{"colour", `"red"`, pick.Answer{}, "unknown field"},
	}
	for _, tc := range cases {
		got, err := pick.StoredAnswer(mo, fileformat.Answer{Field: tc.field, Value: []byte(tc.raw)})
		if tc.err != "" {
			assert.ErrorContains(t, err, tc.err, tc.field+" "+tc.raw)
			continue
		}
		require.NoError(t, err, tc.field+" "+tc.raw)
		assert.Equal(t, tc.want, got, tc.field+" "+tc.raw)
	}
}

func TestExpectedSizeCountsTheNoneGroup(t *testing.T) {
	// a: five items with v and three without, expected (25 + 9) / 8 = 4.25.
	// b: six p and two q, expected (36 + 4) / 8 = 5. Without the "none"
	// group a would also be 5 and lose the tie to b, which comes first.
	a := schema.Field{Name: "a", Type: schema.Category, Role: schema.Preference, Askable: true}
	b := schema.Field{Name: "b", Type: schema.Category, Role: schema.Preference, Askable: true}
	var items []fileformat.Item
	for i := range 8 {
		facts := map[string]schema.Value{"b": cat("p")}
		if i >= 6 {
			facts["b"] = cat("q")
		}
		if i < 5 {
			facts["a"] = cat("v")
		}
		items = append(items, fileformat.Item{ID: string(rune('a' + i)), Facts: facts})
	}
	q, ok := pick.New(model(t, []schema.Field{b, a}, items, nil)).Next()
	require.True(t, ok)
	assert.Equal(t, "a", q.Field.Name)
}

func TestUndoneOtherIsNotAskedAgain(t *testing.T) {
	var items []fileformat.Item
	for i, theme := range []string{"p", "p", "p", "p", "q", "q", "q", "q"} {
		items = append(items, fileformat.Item{ID: string(rune('a' + i)), Facts: map[string]schema.Value{"theme": cat(theme)}})
	}
	s := pick.New(model(t, []schema.Field{themeF}, items, nil))
	q, ok := s.Next()
	require.True(t, ok)
	out, err := s.Apply(pick.Answer{Field: "theme", Kind: pick.Other, Shown: keys(q)})
	require.NoError(t, err)
	require.NotNil(t, out.Empty, "every item has a shown value, so other leaves nothing")
	assert.Equal(t, "does not match the answer to theme (none of p, q)", out.Empty.Causes[0].Cause.String())
	s.Undo()
	assert.Equal(t, 8, s.Remaining())
	_, ok = s.Next()
	assert.False(t, ok, "the undone field counts as no preference and is not asked again")
}

func TestGroupSizeAskedFirst(t *testing.T) {
	players := fitsF
	players.GroupSize = true
	s := pick.New(model(t, []schema.Field{weightF, players}, shelf(), nil))
	q, ok := s.Next()
	require.True(t, ok)
	assert.Equal(t, "fits", q.Field.Name, "the group-size field comes first, though weight splits better")
	_, err := s.Apply(pick.Answer{Field: "fits", Key: "2"})
	require.NoError(t, err)
	q, ok = s.Next()
	require.True(t, ok)
	assert.Equal(t, "weight", q.Field.Name, "then the expected-size rule")

	players.Askable = false
	q, _ = pick.New(model(t, []schema.Field{weightF, players}, shelf(), nil)).Next()
	assert.Equal(t, "weight", q.Field.Name, "not askable, not asked")

	items := shelf()[:5]
	for _, it := range items {
		it.Facts["fits"] = fits(2, 2)
	}
	players.Askable = true
	q, ok = pick.New(model(t, []schema.Field{weightF, players}, items, nil)).Next()
	require.True(t, ok)
	assert.Equal(t, "weight", q.Field.Name, "a group-size field that does not split is skipped")
}
