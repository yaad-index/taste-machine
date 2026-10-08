package check_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/check"
	"github.com/yaad-index/taste-machine/dataset"
	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
	"github.com/yaad-index/taste-machine/score"
)

const eps = 1e-9

var (
	tagsF   = schema.Field{Name: "tags", Type: schema.Set, Role: schema.Both}
	themeF  = schema.Field{Name: "theme", Type: schema.Category, Role: schema.Preference, Weight: 2}
	weightF = schema.Field{Name: "weight", Type: schema.Number, Role: schema.Preference, Edges: []float64{2, 3, 4}}
	bestF   = schema.Field{Name: "best", Type: schema.Votes, Role: schema.Preference}
	soloF   = schema.Field{Name: "solo", Type: schema.Bool, Role: schema.Filter}
	noteF   = schema.Field{Name: "note", Type: schema.Category, Role: schema.Info}
)

func set(v ...string) schema.Value { return schema.Value{Type: schema.Set, Set: v} }

func cat(v string) schema.Value { return schema.Value{Type: schema.Category, Category: v} }

func num(v float64) schema.Value { return schema.Value{Type: schema.Number, Number: v} }

func votes(m map[string]float64) schema.Value { return schema.Value{Type: schema.Votes, Votes: m} }

func r(v float64) *float64 { return &v }

func fields() []schema.Field { return []schema.Field{tagsF, themeF, weightF, bestF, soloF, noteF} }

func load(t *testing.T, shelf, acq []fileformat.Item, meta *fileformat.TasteMeta, taste ...fileformat.TasteItem) *score.Model {
	t.Helper()
	m := fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindCatalogue, Schema: schema.Schema{Fields: fields()}}
	a := m
	a.Kind = fileformat.KindAcquisition
	played := schema.Field{Name: "played", Type: schema.Bool, Role: schema.Both}
	tf := &fileformat.Taste{
		Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindTaste, Taste: meta, Schema: schema.Schema{Fields: []schema.Field{played}}},
		Items: taste,
	}
	d, err := dataset.Load(&fileformat.Catalogue{Meta: m, Items: shelf}, []dataset.Input{{Taste: tf, Name: "me"}}, &fileformat.Catalogue{Meta: a, Items: acq})
	require.NoError(t, err)
	return score.Learn(d, d.Members[0])
}

func item(id string, facts map[string]schema.Value) fileformat.Item {
	return fileformat.Item{ID: id, Facts: facts}
}

func TestSimilarityPerType(t *testing.T) {
	mo := load(t, nil, nil, nil)
	a := score.Item{ID: "a", Facts: map[string]schema.Value{
		"tags": set("x", "y", "z"), "theme": cat("sea"), "weight": num(1.5), "best": votes(map[string]float64{"2": 3, "3": 1}),
		"solo": {Type: schema.Bool, Bool: true}, "note": cat("n1"),
	}}
	b := score.Item{ID: "b", Facts: map[string]schema.Value{
		"tags": set("y", "z", "w"), "theme": cat("sea"), "weight": num(3.5), "best": votes(map[string]float64{"2": 1, "3": 1}),
		"solo": {Type: schema.Bool, Bool: false}, "note": cat("n2"),
	}}
	// tags: Jaccard 2/4, weight 1; theme: equal, weight 2; weight: buckets
	// 0 and 2 of 4, 1 - 2/4; best: shares (.75, .25) vs (.5, .5), TV .25.
	// solo is filter-only and note is info: neither counts.
	want := (1*0.5 + 2*1 + 1*0.5 + 1*0.75) / 5
	assert.InDelta(t, want, check.Similarity(mo, a, b), eps)
	assert.InDelta(t, want, check.Similarity(mo, b, a), eps, "symmetric")
	assert.InDelta(t, 1.0, check.Similarity(mo, a, a), eps)
}

func TestSimilarityMissingFields(t *testing.T) {
	mo := load(t, nil, nil, nil)
	a := score.Item{Facts: map[string]schema.Value{"tags": set("x"), "theme": cat("sea")}}
	b := score.Item{Facts: map[string]schema.Value{"tags": set("x")}}
	assert.InDelta(t, 1.0/3, check.Similarity(mo, a, b), eps, "theme is on one item only: counted, scoring 0")
	assert.InDelta(t, 0.0, check.Similarity(mo, score.Item{}, score.Item{}), eps, "nothing to compare")
	e := score.Item{Facts: map[string]schema.Value{"best": votes(map[string]float64{"2": 0})}}
	f := score.Item{Facts: map[string]schema.Value{"best": votes(map[string]float64{"2": 4})}}
	assert.InDelta(t, 0.0, check.Similarity(mo, e, f), eps, "no votes against some votes is as far as it gets")
	assert.InDelta(t, 1.0, check.Similarity(mo, e, e), eps)
	g := score.Item{Facts: map[string]schema.Value{"tags": set()}}
	assert.InDelta(t, 1.0, check.Similarity(mo, g, g), eps, "two empty sets are the same")
}

func TestSimilarityLeavesOutPerUserFields(t *testing.T) {
	mo := load(t, nil, nil, nil)
	a := score.Item{Facts: map[string]schema.Value{"tags": set("x")}, Entry: &fileformat.TasteItem{Fields: map[string]schema.Value{"played": {Type: schema.Bool, Bool: true}}}}
	b := score.Item{Facts: map[string]schema.Value{"tags": set("x")}}
	assert.InDelta(t, 1.0, check.Similarity(mo, a, b), eps)
}

func TestRun(t *testing.T) {
	shelf := []fileformat.Item{
		item("s1", map[string]schema.Value{"tags": set("x", "y"), "theme": cat("sea")}),
		item("s2", map[string]schema.Value{"tags": set("x"), "theme": cat("sea")}),
		item("s3", map[string]schema.Value{"tags": set("x", "y"), "theme": cat("sea")}),
		item("s4", map[string]schema.Value{"tags": set("q"), "theme": cat("sea")}),
		item("s5", map[string]schema.Value{"tags": set("x", "y"), "theme": cat("space")}),
		item("dup", map[string]schema.Value{"tags": set("x", "y"), "theme": cat("sea")}),
	}
	acq := []fileformat.Item{
		item("new", map[string]schema.Value{"tags": set("x", "y"), "theme": cat("sea")}),
		item("dup", map[string]schema.Value{"tags": set("x", "y"), "theme": cat("sea")}),
		item("meh", map[string]schema.Value{"tags": set("q"), "theme": cat("space")}),
		item("bad", map[string]schema.Value{"tags": set("x", "y"), "theme": cat("sea"), "solo": {Type: schema.Bool, Bool: true}}),
		item("nope", map[string]schema.Value{"tags": set("x")}),
		item("fav", map[string]schema.Value{"tags": set("q")}),
	}
	mo := load(t, shelf, acq, &fileformat.TasteMeta{
		Blocked:     []string{"nope"},
		Favourites:  []string{"fav"},
		Preferences: map[string]map[string]fileformat.Stance{"solo": {"true": fileformat.Never}},
	}, fileformat.TasteItem{ID: "s1", Rating: r(10)}, fileformat.TasteItem{ID: "s4", Rating: r(2)})

	reports := check.Run(mo, score.DefaultSimilarityThreshold)
	var order []string
	for _, rp := range reports {
		order = append(order, rp.ID)
	}
	assert.Equal(t, []string{"fav", "dup", "new", "meh", "bad", "nope"}, order, "scored items ranked, then excluded ones in id order")

	byID := map[string]check.Report{}
	for _, rp := range reports {
		byID[rp.ID] = rp
	}
	fav := byID["fav"]
	assert.True(t, fav.Result.Favourite)
	assert.InDelta(t, 1.0, fav.Result.TasteScore, eps)

	n := byID["new"]
	assert.Nil(t, n.Excluded)
	assert.False(t, n.AlreadyInCatalogue)
	// new vs s1, s3, dup: identical (1); s2: tags 1/2, theme 1 → (0.5+2)/3;
	// s5: tags 1, theme 0 → 1/3; s4: theme only → 2/3.
	assert.Equal(t, 5, n.LikeThis)
	assert.Equal(t, []check.Neighbour{{ID: "dup", Similarity: 1}, {ID: "s1", Similarity: 1}, {ID: "s3", Similarity: 1}}, n.Closest)

	d := byID["dup"]
	assert.True(t, d.AlreadyInCatalogue)
	assert.Equal(t, 4, d.LikeThis, "its own shelf copy is not its neighbour")
	assert.NotContains(t, d.Closest, check.Neighbour{ID: "dup", Similarity: 1})

	assert.Equal(t, &score.Cause{Kind: score.ByList}, byID["nope"].Excluded, "blocked: excluded, not scored")
	assert.Equal(t, score.Result{}, byID["nope"].Result)
	assert.Equal(t, &score.Cause{Kind: score.ByFilter, Field: "solo", Value: "true"}, byID["bad"].Excluded, "hard filters apply in check")
	assert.Positive(t, byID["bad"].LikeThis, "an excluded item still gets its neighbours")
}

func TestRunThreshold(t *testing.T) {
	shelf := []fileformat.Item{
		item("a", map[string]schema.Value{"tags": set("x", "y")}),
		item("b", map[string]schema.Value{"tags": set("x", "y", "z")}),
	}
	acq := []fileformat.Item{item("n", map[string]schema.Value{"tags": set("x", "y")})}
	mo := load(t, shelf, acq, nil)
	rp := check.Run(mo, 0.7)
	require.Len(t, rp, 1)
	assert.Equal(t, 1, rp[0].LikeThis, "2/3 is below 0.7")
	rp = check.Run(mo, 2.0/3)
	assert.Equal(t, 2, rp[0].LikeThis, "the threshold is inclusive")
}
