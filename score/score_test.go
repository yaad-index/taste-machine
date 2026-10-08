package score_test

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/dataset"
	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
	"github.com/yaad-index/taste-machine/score"
)

var update = flag.Bool("update", false, "rewrite golden files")

const eps = 1e-9

func set(vals ...string) schema.Value { return schema.Value{Type: schema.Set, Set: vals} }

func num(v float64) schema.Value { return schema.Value{Type: schema.Number, Number: v} }

func votes(v map[string]float64) schema.Value { return schema.Value{Type: schema.Votes, Votes: v} }

func r(v float64) *float64 { return &v }

type fixture struct {
	fields []schema.Field
	shelf  []fileformat.Item
	learn  []fileformat.Item
	meta   *fileformat.TasteMeta
	items  []fileformat.TasteItem
	user   []schema.Field
}

func (fx fixture) load(t *testing.T) (*dataset.Dataset, *score.Model) {
	t.Helper()
	s := schema.Schema{Fields: fx.fields}
	shelf := &fileformat.Catalogue{
		Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindCatalogue, Schema: s},
		Items: fx.shelf,
	}
	tf := &fileformat.Taste{
		Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindTaste, Taste: fx.meta, Schema: schema.Schema{Fields: fx.user}},
		Items: fx.items,
	}
	if fx.learn != nil {
		tf.LearnFrom = &fileformat.Catalogue{Meta: shelf.Meta, Items: fx.learn}
	}
	d, err := dataset.Load(shelf, []dataset.Input{{Taste: tf, Name: "me"}}, nil)
	require.NoError(t, err)
	return d, score.Learn(d, d.Members[0])
}

var tagsField = schema.Field{Name: "tags", Type: schema.Set, Role: schema.Both}

// basic: ratings on a 1-10 scale; a=10 and b=4 give mean 7, so a's signal
// is (10-7)/(10-7) = 1 and b's is (4-7)/(7-1) = -0.5.
func basic() fixture {
	return fixture{
		fields: []schema.Field{tagsField},
		shelf: []fileformat.Item{
			{ID: "a", Facts: map[string]schema.Value{"tags": set("x", "y")}},
			{ID: "b", Facts: map[string]schema.Value{"tags": set("x")}},
			{ID: "c", Facts: map[string]schema.Value{"tags": set("y")}},
			{ID: "d", Facts: map[string]schema.Value{"tags": set("x", "z")}},
		},
		items: []fileformat.TasteItem{
			{ID: "a", Rating: r(10)},
			{ID: "b", Rating: r(4)},
			{ID: "d"},
		},
	}
}

func TestAffinity(t *testing.T) {
	_, mo := basic().load(t)
	assert.InDelta(t, 7.0, mo.Mean, eps)
	// x: (1 - 0.5) / (2 + 3); d has neither a rating nor plays, so it is
	// not in n.
	assert.InDelta(t, 0.1, mo.Affinity("tags", "x"), eps)
	assert.InDelta(t, 0.25, mo.Affinity("tags", "y"), eps)
	assert.InDelta(t, 0.0, mo.Affinity("tags", "z"), eps, "no history, no affinity")

	res := mo.Score(mo.Item("c"))
	assert.InDelta(t, 0.25, res.TasteScore, eps)
	res = mo.Score(mo.Item("d"))
	assert.InDelta(t, 0.05, res.TasteScore, eps, "an unknown value counts as 0 inside the field mean")
}

func TestSignal(t *testing.T) {
	_, mo := basic().load(t)
	cases := []struct {
		name string
		e    fileformat.TasteItem
		want float64
	}{
		{"top of scale", fileformat.TasteItem{Rating: r(10)}, 1},
		{"bottom of scale", fileformat.TasteItem{Rating: r(1)}, -1},
		{"at the mean", fileformat.TasteItem{Rating: r(7)}, 0},
		{"above the mean", fileformat.TasteItem{Rating: r(8.5)}, 0.5},
		{"below the mean", fileformat.TasteItem{Rating: r(4)}, -0.5},
		{"rating wins over plays", fileformat.TasteItem{Rating: r(7), Plays: 50}, 0},
		{"no rating, no plays", fileformat.TasteItem{}, 0},
		{"one play is tried, not liked", fileformat.TasteItem{Plays: 1}, 0},
		{"two plays", fileformat.TasteItem{Plays: 2}, 0.5 * 1 / math.Log2(10)},
		{"saturation", fileformat.TasteItem{Plays: 10}, 0.5},
		{"past saturation", fileformat.TasteItem{Plays: 40}, 0.5},
	}
	for _, tc := range cases {
		assert.InDelta(t, tc.want, mo.Signal(tc.e), eps, tc.name)
	}
}

func TestSignalZeroDenominators(t *testing.T) {
	fx := basic()
	fx.items = []fileformat.TasteItem{{ID: "a", Rating: r(10)}, {ID: "b", Rating: r(10)}}
	_, mo := fx.load(t)
	assert.InDelta(t, 0.0, mo.Signal(fileformat.TasteItem{Rating: r(10)}), eps, "hi - m = 0")

	fx.items = []fileformat.TasteItem{{ID: "a", Rating: r(1)}, {ID: "b", Rating: r(1)}}
	_, mo = fx.load(t)
	assert.InDelta(t, 0.0, mo.Signal(fileformat.TasteItem{Rating: r(1)}), eps, "m - lo = 0 and r = m")
	assert.InDelta(t, 1.0, mo.Signal(fileformat.TasteItem{Rating: r(10)}), eps)
	assert.InDelta(t, 0.0, mo.Affinity("tags", "x"), eps, "every rating equal gives no signal")
}

func TestLowerBetterScale(t *testing.T) {
	fx := basic()
	fx.meta = &fileformat.TasteMeta{Scale: &fileformat.Scale{Min: 1, Max: 5, Direction: fileformat.LowerBetter}}
	fx.items = []fileformat.TasteItem{{ID: "a", Rating: r(1)}, {ID: "b", Rating: r(5)}}
	_, mo := fx.load(t)
	assert.InDelta(t, 3.0, mo.Mean, eps)
	assert.InDelta(t, 1.0, mo.Signal(fileformat.TasteItem{Rating: r(1)}), eps, "the best rating on a lower-better scale is 1")
	rating, ok := mo.Rating(&fileformat.TasteItem{Rating: r(2)})
	assert.True(t, ok)
	assert.InDelta(t, 4.0, rating, eps)
}

func TestPlaysCountInN(t *testing.T) {
	fx := basic()
	fx.items = append(fx.items, fileformat.TasteItem{ID: "c", Plays: 1})
	_, mo := fx.load(t)
	// c has y and one play: signal 0, but it is history, so n for y is 2.
	assert.InDelta(t, 1.0/5, mo.Affinity("tags", "y"), eps)
}

func TestVotesAffinityAndScore(t *testing.T) {
	best := schema.Field{Name: "best", Type: schema.Votes, Role: schema.Preference}
	fx := fixture{
		fields: []schema.Field{best},
		shelf: []fileformat.Item{
			{ID: "a", Facts: map[string]schema.Value{"best": votes(map[string]float64{"3": 3, "4": 1})}},
			{ID: "b", Facts: map[string]schema.Value{"best": votes(map[string]float64{"3": 1, "4": 1})}},
			{ID: "z", Facts: map[string]schema.Value{"best": votes(map[string]float64{"3": 0})}},
		},
		items: []fileformat.TasteItem{{ID: "a", Rating: r(10)}, {ID: "z", Rating: r(4)}},
	}
	_, mo := fx.load(t)
	// mean 7: a's signal 1, z's -0.5 but z has no votes, so it adds nothing.
	assert.InDelta(t, 0.75/3.75, mo.Affinity("best", "3"), eps)
	assert.InDelta(t, 0.25/3.25, mo.Affinity("best", "4"), eps)
	res := mo.Score(mo.Item("b"))
	assert.InDelta(t, 0.5*0.75/3.75+0.5*0.25/3.25, res.TasteScore, eps, "the field score weights values by share")
	assert.InDelta(t, 0.0, mo.Score(mo.Item("z")).TasteScore, eps, "a votes field with no votes counts as missing")
}

func TestZeroVoteWithZeroK(t *testing.T) {
	best := schema.Field{Name: "best", Type: schema.Votes, Role: schema.Preference}
	fx := fixture{
		fields: []schema.Field{best},
		shelf:  []fileformat.Item{{ID: "a", Facts: map[string]schema.Value{"best": votes(map[string]float64{"3": 2, "4": 0})}}},
		meta:   &fileformat.TasteMeta{Constants: fileformat.Constants{K: r(0)}},
		items:  []fileformat.TasteItem{{ID: "a", Plays: 10}},
	}
	_, mo := fx.load(t)
	res := mo.Score(mo.Item("a"))
	assert.False(t, math.IsNaN(res.TasteScore), "a value with no votes must not reach 0 / 0")
	assert.InDelta(t, 0.5, res.TasteScore, eps)
}

func TestNumberBuckets(t *testing.T) {
	weight := schema.Field{Name: "weight", Type: schema.Number, Role: schema.Preference, Edges: []float64{2, 3}}
	fx := fixture{
		fields: []schema.Field{weight},
		shelf: []fileformat.Item{
			{ID: "a", Facts: map[string]schema.Value{"weight": num(1.5)}},
			{ID: "b", Facts: map[string]schema.Value{"weight": num(1.9)}},
			{ID: "c", Facts: map[string]schema.Value{"weight": num(3.4)}},
		},
		learn: []fileformat.Item{{ID: "old", Facts: map[string]schema.Value{"weight": num(0.5)}}},
		items: []fileformat.TasteItem{{ID: "a", Rating: r(10)}, {ID: "c", Rating: r(4)}, {ID: "old", Rating: r(7)}},
	}
	_, mo := fx.load(t)
	assert.Equal(t, score.Edges{2, 3}, mo.Edges["weight"])
	// mean 7: a is 1, old is 0 (both bucket 0), c is -0.5 (bucket 2).
	assert.InDelta(t, 1.0/5, mo.Affinity("weight", "0"), eps, "learn-from items are bucketed with the shelf's edges")
	assert.InDelta(t, -0.5/4, mo.Affinity("weight", "2"), eps)
	res := mo.Score(mo.Item("b"))
	assert.InDelta(t, 0.2, res.TasteScore, eps)
	require.Len(t, res.Contributions, 1)
	assert.Equal(t, score.Contribution{Field: "weight", Value: "< 2", Amount: 0.2}, res.Contributions[0])
}

func TestStatedPreferencesReplaceAffinity(t *testing.T) {
	fx := basic()
	fx.meta = &fileformat.TasteMeta{Preferences: map[string]map[string]fileformat.Stance{
		"tags": {"x": fileformat.Dislike, "z": fileformat.Like},
	}}
	_, mo := fx.load(t)
	assert.InDelta(t, -1.0, mo.Affinity("tags", "x"), eps)
	assert.InDelta(t, 1.0, mo.Affinity("tags", "z"), eps)
	assert.InDelta(t, 0.25, mo.Affinity("tags", "y"), eps, "values without a stance keep their learned affinity")
}

func TestMissingFieldCountsAsZero(t *testing.T) {
	theme := schema.Field{Name: "theme", Type: schema.Category, Role: schema.Preference, Weight: 3}
	fx := basic()
	fx.fields = append(fx.fields, theme)
	_, mo := fx.load(t)
	// tags weight 1, theme weight 3: c has y (0.25) and no theme.
	assert.InDelta(t, 0.25/4, mo.Score(mo.Item("c")).TasteScore, eps)
}

func TestInfoAndFilterFieldsAreNotScored(t *testing.T) {
	fx := basic()
	fx.fields = []schema.Field{{Name: "tags", Type: schema.Set, Role: schema.Filter}, {Name: "note", Type: schema.Set, Role: schema.Info}}
	for i := range fx.shelf {
		fx.shelf[i].Facts["note"] = set("x")
	}
	_, mo := fx.load(t)
	res := mo.Score(mo.Item("a"))
	assert.InDelta(t, 0.0, res.TasteScore, eps)
	assert.Empty(t, res.Contributions)
}

func TestFavourites(t *testing.T) {
	fx := basic()
	fx.meta = &fileformat.TasteMeta{Favourites: []string{"b"}}
	_, mo := fx.load(t)
	res := mo.Score(mo.Item("b"))
	assert.True(t, res.Favourite)
	assert.InDelta(t, 1.0, res.TasteScore, eps)
	assert.InDelta(t, 1.0, res.Final, eps)
	assert.InDelta(t, 0.1, res.Computed, eps, "the history's score is kept for the explanation")
	assert.Contains(t, res.Explain(), "favourite: score shown is 1, history gives 0.100")
}

func TestContributionsSumToComputed(t *testing.T) {
	fx := basic()
	fx.fields = append(fx.fields, schema.Field{Name: "theme", Type: schema.Category, Role: schema.Preference, Weight: 2})
	fx.shelf[0].Facts["theme"] = schema.Value{Type: schema.Category, Category: "space"}
	fx.shelf[3].Facts["theme"] = schema.Value{Type: schema.Category, Category: "space"}
	_, mo := fx.load(t)
	res := mo.Score(mo.Item("d"))
	var sum float64
	for _, c := range res.Contributions {
		sum += c.Amount
	}
	assert.InDelta(t, res.Computed, sum, eps)
	for i := 1; i < len(res.Contributions); i++ {
		assert.GreaterOrEqual(t, res.Contributions[i-1].Amount, res.Contributions[i].Amount, "largest first")
	}
}

func TestPositiveNegative(t *testing.T) {
	res := score.Result{Contributions: []score.Contribution{
		{Field: "f", Value: "a", Amount: 0.4},
		{Field: "f", Value: "b", Amount: 0.3},
		{Field: "f", Value: "c", Amount: 0.2},
		{Field: "f", Value: "d", Amount: 0.1},
		{Field: "f", Value: "e", Amount: -0.1},
		{Field: "f", Value: "g", Amount: -0.2},
		{Field: "f", Value: "h", Amount: -0.3},
	}}
	var pos, neg []string
	for _, c := range res.Positive(3) {
		pos = append(pos, c.Value)
	}
	for _, c := range res.Negative(2) {
		neg = append(neg, c.Value)
	}
	assert.Equal(t, []string{"a", "b", "c"}, pos)
	assert.Equal(t, []string{"h", "g"}, neg)
}

func TestConstantsOverride(t *testing.T) {
	fx := basic()
	fx.meta = &fileformat.TasteMeta{Constants: fileformat.Constants{K: r(0), PlayWeight: r(1), PlaySaturation: r(4), AnswerWeight: r(2)}}
	_, mo := fx.load(t)
	assert.Equal(t, score.Constants{K: 0, PlayWeight: 1, PlaySaturation: 4, AnswerWeight: 2}, mo.Constants)
	assert.InDelta(t, 0.25, mo.Affinity("tags", "x"), eps, "k = 0: (1 - 0.5) / 2")
	assert.InDelta(t, 0.5, mo.Signal(fileformat.TasteItem{Plays: 2}), eps, "log2(2) / log2(4)")
	assert.Equal(t, score.Defaults(), score.For(&dataset.Member{}))
}

func TestFilters(t *testing.T) {
	played := schema.Field{Name: "played", Type: schema.Bool, Role: schema.Filter}
	theme := schema.Field{Name: "theme", Type: schema.Category, Role: schema.Filter, Missing: schema.Drop}
	solo := schema.Field{Name: "solo", Type: schema.Bool, Role: schema.Filter}
	fx := basic()
	fx.fields = append(fx.fields, theme, solo)
	fx.user = []schema.Field{played}
	fx.shelf[0].Facts["theme"] = schema.Value{Type: schema.Category, Category: "space"}
	fx.shelf[1].Facts["theme"] = schema.Value{Type: schema.Category, Category: "sea"}
	fx.shelf[2].Facts["theme"] = schema.Value{Type: schema.Category, Category: "sea"}
	fx.shelf = append(fx.shelf, fileformat.Item{ID: "e", Facts: map[string]schema.Value{"tags": set("q"), "theme": {Type: schema.Category, Category: "sea"}}})
	fx.shelf = append(fx.shelf, fileformat.Item{ID: "f", Facts: map[string]schema.Value{"tags": set("q")}})
	fx.items = []fileformat.TasteItem{
		{ID: "a", Rating: r(10), Fields: map[string]schema.Value{"played": {Type: schema.Bool, Bool: true}}},
		{ID: "b", Rating: r(4), Fields: map[string]schema.Value{"played": {Type: schema.Bool, Bool: true}}},
	}
	fx.meta = &fileformat.TasteMeta{
		Blocked: []string{"c"},
		Preferences: map[string]map[string]fileformat.Stance{
			"theme":  {"space": fileformat.Never},
			"played": {"true": fileformat.Never},
			"tags":   {"z": fileformat.Never},
		},
	}
	_, mo := fx.load(t)
	out := mo.ScoreAll(mo.ShelfItems())
	require.Len(t, out.Results, 1)
	assert.Equal(t, "e", out.Results[0].ID)
	assert.Equal(t, []score.Exclusion{
		{ID: "a", Cause: score.Cause{Kind: score.ByFilter, Field: "theme", Value: "space"}},
		{ID: "b", Cause: score.Cause{Kind: score.ByFilter, Field: "played", Value: "true"}},
		{ID: "c", Cause: score.Cause{Kind: score.ByList}},
		{ID: "d", Cause: score.Cause{Kind: score.ByFilter, Field: "tags", Value: "z"}},
		{ID: "f", Cause: score.Cause{Kind: score.ByFilter, Field: "theme"}},
	}, out.Excluded, "a per-user field filters through the same path; f lacks theme, whose filter drops missing items")
	assert.Equal(t, []string{"not on the blocked list", "tags is not z", "theme is not space", "played is not true"}, out.Results[0].Passed, "filters in schema order, per-user fields last")
	assert.Nil(t, out.Empty)
}

func TestMissingKeepPassesFilter(t *testing.T) {
	fx := basic()
	fx.fields = append(fx.fields, schema.Field{Name: "theme", Type: schema.Category, Role: schema.Filter})
	fx.shelf[0].Facts["theme"] = schema.Value{Type: schema.Category, Category: "space"}
	fx.meta = &fileformat.TasteMeta{Preferences: map[string]map[string]fileformat.Stance{"theme": {"space": fileformat.Never}}}
	_, mo := fx.load(t)
	out := mo.ScoreAll(mo.ShelfItems())
	assert.Len(t, out.Results, 3, "items without theme pass under the default keep policy")
	assert.Len(t, out.Excluded, 1)
}

func TestEmptyReport(t *testing.T) {
	fx := basic()
	fx.meta = &fileformat.TasteMeta{
		Blocked:     []string{"a", "c"},
		Preferences: map[string]map[string]fileformat.Stance{"tags": {"x": fileformat.Never}},
	}
	_, mo := fx.load(t)
	out := mo.ScoreAll(mo.ShelfItems())
	assert.Empty(t, out.Results)
	require.NotNil(t, out.Empty)
	assert.Equal(t, []score.CauseCount{
		{Cause: score.Cause{Kind: score.ByFilter, Field: "tags", Value: "x"}, Items: 2},
		{Cause: score.Cause{Kind: score.ByList}, Items: 2},
	}, out.Empty.Causes)
	assert.Equal(t, "on the blocked list", out.Empty.Causes[1].Cause.String())
	assert.Equal(t, "tags is x, marked never", out.Empty.Causes[0].Cause.String())
	assert.Equal(t, "no theme, and the filter on it drops items without it", score.Cause{Kind: score.ByFilter, Field: "theme"}.String())
}

func TestRank(t *testing.T) {
	results := []score.Result{
		{ID: "e", Final: 0.5},
		{ID: "d", Final: 0.5, HasRated: true, Rating: 6},
		{ID: "c", Final: 0.5, HasRated: true, Rating: 8},
		{ID: "a", Final: 0.5},
		{ID: "b", Final: 0.9},
		{ID: "f", Final: -0.2, HasRated: true, Rating: 10},
	}
	score.Rank(results)
	var ids []string
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"b", "c", "d", "a", "e", "f"}, ids)
}

func TestBuckets(t *testing.T) {
	assert.Equal(t, score.Edges{3, 5, 7, 9}, score.QuantileEdges([]float64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}, 5))
	assert.Equal(t, score.Edges{2, 3}, score.QuantileEdges([]float64{1, 1, 2, 3, 3}, 5), "three distinct values give three buckets")
	assert.Empty(t, score.QuantileEdges([]float64{4, 4, 4}, 5), "one distinct value is one bucket")
	assert.Empty(t, score.QuantileEdges(nil, 5))
	e := score.Edges{2, 3}
	assert.Equal(t, 3, e.Count())
	assert.Equal(t, 0, e.Bucket(1.9))
	assert.Equal(t, 1, e.Bucket(2))
	assert.Equal(t, 2, e.Bucket(3))
	assert.Equal(t, 2, e.Bucket(99))
	assert.Equal(t, "< 2", e.Label(0))
	assert.Equal(t, "2 to < 3", e.Label(1))
	assert.Equal(t, ">= 3", e.Label(2))
	assert.Equal(t, "all", score.Edges{}.Label(0))
}

func TestQuantileBucketsFromShelf(t *testing.T) {
	weight := schema.Field{Name: "weight", Type: schema.Number, Role: schema.Preference, Buckets: 2}
	fx := fixture{
		fields: []schema.Field{weight},
		shelf: []fileformat.Item{
			{ID: "a", Facts: map[string]schema.Value{"weight": num(1)}},
			{ID: "b", Facts: map[string]schema.Value{"weight": num(2)}},
			{ID: "c", Facts: map[string]schema.Value{"weight": num(3)}},
			{ID: "d", Facts: map[string]schema.Value{"weight": num(4)}},
		},
		learn: []fileformat.Item{{ID: "far", Facts: map[string]schema.Value{"weight": num(100)}}},
		items: []fileformat.TasteItem{{ID: "far", Rating: r(5)}},
	}
	_, mo := fx.load(t)
	assert.Equal(t, score.Edges{3}, mo.Edges["weight"], "edges come from the shelf only, with the declared bucket count")
}

// TestGolden pins the full ranked output for a fixed input, so the same
// files always give the same output.
func TestGolden(t *testing.T) {
	theme := schema.Field{Name: "theme", Type: schema.Category, Role: schema.Preference, Weight: 2}
	weight := schema.Field{Name: "weight", Type: schema.Number, Role: schema.Preference, Buckets: 3}
	best := schema.Field{Name: "best", Type: schema.Votes, Role: schema.Preference}
	fx := fixture{
		fields: []schema.Field{tagsField, theme, weight, best},
		meta: &fileformat.TasteMeta{
			Favourites:  []string{"g"},
			Blocked:     []string{"h"},
			Preferences: map[string]map[string]fileformat.Stance{"tags": {"q": fileformat.Never, "w": fileformat.Like}},
		},
	}
	themes := []string{"sea", "space", "forest"}
	for i, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		fx.shelf = append(fx.shelf, fileformat.Item{ID: id, Facts: map[string]schema.Value{
			"tags":   set([]string{"u", "v", "w", "x", "y"}[i%5], []string{"u", "v", "w", "x", "y"}[(i+2)%5]),
			"theme":  {Type: schema.Category, Category: themes[i%3]},
			"weight": num(float64(i%4) + 1.5),
			"best":   votes(map[string]float64{"2": float64(i % 3), "4": float64(3 - i%3)}),
		}})
	}
	fx.shelf[9].Facts["tags"] = set("q")
	fx.items = []fileformat.TasteItem{
		{ID: "a", Rating: r(9)},
		{ID: "b", Rating: r(3)},
		{ID: "c", Rating: r(7.5)},
		{ID: "d", Plays: 12},
		{ID: "e", Plays: 1},
		{ID: "f"},
	}
	_, mo := fx.load(t)
	out := mo.ScoreAll(mo.ShelfItems())
	var b strings.Builder
	for _, res := range out.Results {
		b.WriteString(res.Explain())
	}
	for _, e := range out.Excluded {
		b.WriteString("excluded " + e.ID + ": " + e.Cause.String() + "\n")
	}
	got := b.String()
	golden := filepath.Join("testdata", "golden.txt")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}
