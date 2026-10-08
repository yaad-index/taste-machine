package group_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/check"
	"github.com/yaad-index/taste-machine/dataset"
	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/group"
	"github.com/yaad-index/taste-machine/pick"
	"github.com/yaad-index/taste-machine/schema"
	"github.com/yaad-index/taste-machine/score"
)

const eps = 1e-9

var (
	playersF = schema.Field{Name: "players", Type: schema.Range, Role: schema.Filter, Askable: true, GroupSize: true}
	tagsF    = schema.Field{Name: "tags", Type: schema.Set, Role: schema.Both, Askable: true}
	themeF   = schema.Field{Name: "theme", Type: schema.Category, Role: schema.Preference, Askable: true}
	playedF  = schema.Field{Name: "played", Type: schema.Bool, Role: schema.Filter, Askable: true}
)

func r(v float64) *float64 { return &v }

func fits(lo, hi float64) schema.Value {
	return schema.Value{Type: schema.Range, Range: schema.RangeValue{Min: lo, Max: hi}}
}

func set(v ...string) schema.Value { return schema.Value{Type: schema.Set, Set: v} }

func cat(v string) schema.Value { return schema.Value{Type: schema.Category, Category: v} }

func shelf() []fileformat.Item {
	return []fileformat.Item{
		{ID: "a", Facts: map[string]schema.Value{"players": fits(2, 4), "tags": set("x"), "theme": cat("sea")}},
		{ID: "b", Facts: map[string]schema.Value{"players": fits(2, 2), "tags": set("y"), "theme": cat("sea")}},
		{ID: "c", Facts: map[string]schema.Value{"players": fits(1, 5), "tags": set("x", "y"), "theme": cat("space")}},
		{ID: "d", Facts: map[string]schema.Value{"players": fits(3, 6), "tags": set("z"), "theme": cat("space")}},
		{ID: "e", Facts: map[string]schema.Value{"tags": set("x"), "theme": cat("forest")}},
	}
}

type member struct {
	name  string
	meta  *fileformat.TasteMeta
	items []fileformat.TasteItem
}

func load(t *testing.T, items []fileformat.Item, acq []fileformat.Item, members ...member) *dataset.Dataset {
	t.Helper()
	m := fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindCatalogue, Schema: schema.Schema{Fields: []schema.Field{playersF, tagsF, themeF}}}
	var inputs []dataset.Input
	for _, mb := range members {
		inputs = append(inputs, dataset.Input{Name: mb.name, Taste: &fileformat.Taste{
			Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindTaste, Taste: mb.meta, Schema: schema.Schema{Fields: []schema.Field{playedF}}},
			Items: mb.items,
		}})
	}
	var a *fileformat.Catalogue
	if acq != nil {
		am := m
		am.Kind = fileformat.KindAcquisition
		a = &fileformat.Catalogue{Meta: am, Items: acq}
	}
	d, err := dataset.Load(&fileformat.Catalogue{Meta: m, Items: items}, inputs, a)
	require.NoError(t, err)
	return d
}

// two members on a 1-10 scale. ann rates a 10 and c 4 (mean 7: a is +1,
// c is -0.5); bob rates c 10 and b 4 (c is +1, b is -0.5).
func two(t *testing.T, annMeta, bobMeta *fileformat.TasteMeta) *dataset.Dataset {
	return load(t, shelf(), nil,
		member{"bob", bobMeta, []fileformat.TasteItem{{ID: "c", Rating: r(10)}, {ID: "b", Rating: r(4)}}},
		member{"ann", annMeta, []fileformat.TasteItem{{ID: "a", Rating: r(10)}, {ID: "c", Rating: r(4)}}},
	)
}

func ids(rs []score.Result) []string {
	var out []string
	for _, x := range rs {
		out = append(out, x.ID)
	}
	return out
}

func TestNew(t *testing.T) {
	g, err := group.New(two(t, nil, nil), 0, 1)
	require.NoError(t, err)
	require.Len(t, g.Members, 2)
	assert.Equal(t, "ann", g.Members[0].Member.Label, "members in label order, not input order")
	assert.InDelta(t, 2.0, g.Size, eps, "size defaults to the number of members")
	assert.InDelta(t, 1.0, g.AnswerWeight(), eps)

	g, err = group.New(two(t, nil, nil), 5, 2.5)
	require.NoError(t, err)
	assert.InDelta(t, 5.0, g.Size, eps)
	assert.InDelta(t, 2.5, g.AnswerWeight(), eps, "B comes from the caller, not the members")

	_, err = group.New(load(t, shelf(), nil, member{"solo", nil, nil}), 0, 1)
	assert.ErrorContains(t, err, "at least 2 taste files")
	_, err = group.New(two(t, nil, nil), -1, 1)
	assert.ErrorContains(t, err, "negative group size")
	_, err = group.New(two(t, nil, nil), 0, -1)
	assert.ErrorContains(t, err, "negative answer weight")
}

func TestFilters(t *testing.T) {
	g, err := group.New(two(t,
		&fileformat.TasteMeta{Blocked: []string{"c"}, Favourites: []string{"a"}},
		&fileformat.TasteMeta{Preferences: map[string]map[string]fileformat.Stance{"tags": {"x": fileformat.Never}}, Favourites: []string{"c"}},
	), 3, 1)
	require.NoError(t, err)
	passed, excluded := g.Filters(g.ShelfItems())
	var kept []string
	for _, it := range passed {
		kept = append(kept, it.ID)
	}
	assert.Equal(t, []string{"d"}, kept)
	assert.Equal(t, []score.Exclusion{
		{ID: "a", Cause: score.Cause{Kind: score.ByFilter, Field: "tags", Value: "x", Member: "bob"}},
		{ID: "b", Cause: score.Cause{Kind: score.ByGroupSize, Field: "players", Value: "3"}},
		{ID: "c", Cause: score.Cause{Kind: score.ByList, Member: "ann"}},
		{ID: "e", Cause: score.Cause{Kind: score.ByFilter, Field: "tags", Value: "x", Member: "bob"}},
	}, excluded, "a member's favourite does not beat another member's filter; a blocked item is a veto even when another member favours it")
	assert.Equal(t, "on the blocked list (ann)", excluded[2].Cause.String())
	assert.Equal(t, "players does not fit 3, the group size", excluded[1].Cause.String())
	assert.Equal(t, []string{"players fits 3, the group size", "ann: not on the blocked list", "bob: tags is not x"}, g.PassedFilters())
}

func TestGroupSizeMissingPolicy(t *testing.T) {
	d := two(t, nil, nil)
	d.Schema.Fields[0].Missing = schema.Drop
	g, err := group.New(d, 0, 1)
	require.NoError(t, err)
	_, excluded := g.Filters(g.ShelfItems())
	assert.Contains(t, excluded, score.Exclusion{ID: "e", Cause: score.Cause{Kind: score.ByGroupSize, Field: "players"}})

	g, err = group.New(two(t, nil, nil), 0, 1)
	require.NoError(t, err)
	passed, _ := g.Filters(g.ShelfItems())
	assert.Len(t, passed, 4, "keep: e has no players and passes; d does not fit 2")
}

func TestPerUserFilterAppliesPerMember(t *testing.T) {
	played := schema.Value{Type: schema.Bool, Bool: true}
	d := load(t, shelf(), nil,
		member{"ann", &fileformat.TasteMeta{Preferences: map[string]map[string]fileformat.Stance{"played": {"true": fileformat.Never}}}, []fileformat.TasteItem{{ID: "e", Plays: 2, Fields: map[string]schema.Value{"played": played}}}},
		member{"bob", nil, []fileformat.TasteItem{{ID: "a", Plays: 2, Fields: map[string]schema.Value{"played": played}}}},
	)
	g, err := group.New(d, 2, 1)
	require.NoError(t, err)
	_, excluded := g.Filters(g.ShelfItems())
	assert.Contains(t, excluded, score.Exclusion{ID: "e", Cause: score.Cause{Kind: score.ByFilter, Field: "played", Value: "true", Member: "ann"}})
	assert.NotContains(t, excluded, score.Exclusion{ID: "a", Cause: score.Cause{Kind: score.ByFilter, Field: "played", Value: "true", Member: "ann"}}, "ann's filter reads ann's entries, not bob's")
	for _, f := range g.QuestionFields() {
		assert.NotEqual(t, "played", f.Name, "per-user fields are not asked")
		assert.NotEqual(t, "players", f.Name, "the group-size field is not asked")
	}
}

func TestScore(t *testing.T) {
	g, err := group.New(two(t, nil, &fileformat.TasteMeta{Favourites: []string{"b"}}), 0, 1)
	require.NoError(t, err)
	ann, bob := g.Members[0], g.Members[1]

	res := g.Score(score.Item{ID: "c", Facts: shelf()[2].Facts})
	wantAnn := ann.Score(ann.View(score.Item{ID: "c", Facts: shelf()[2].Facts})).TasteScore
	wantBob := bob.Score(bob.View(score.Item{ID: "c", Facts: shelf()[2].Facts})).TasteScore
	assert.InDelta(t, (wantAnn+wantBob)/2, res.TasteScore, eps, "the mean of the members' scores")
	assert.InDelta(t, min(wantAnn, wantBob), res.Floor, eps)
	assert.InDelta(t, (3.0/9+1)/2, res.Rating, eps, "mean over the members who rated, each on [0, 1]: 4 and 10 on 1-10")
	assert.True(t, res.HasRated)
	require.Len(t, res.Members, 2)
	assert.Equal(t, "ann", res.Members[0].Label)
	assert.LessOrEqual(t, len(res.Members[0].Positive), 2)
	assert.LessOrEqual(t, len(res.Members[0].Negative), 1)

	res = g.Score(score.Item{ID: "b", Facts: shelf()[1].Facts})
	assert.True(t, res.Members[1].Favourite)
	assert.InDelta(t, 1.0, res.Members[1].Score, eps, "a favourite counts 1 for that member only")
	assert.InDelta(t, (res.Members[0].Score+1)/2, res.TasteScore, eps)
	assert.InDelta(t, 3.0/9, res.Rating, eps, "only bob rated b")
	assert.Contains(t, res.Explain(), "favourite of: bob")

	res = g.Score(score.Item{ID: "d", Facts: shelf()[3].Facts})
	assert.False(t, res.HasRated)
}

func TestRatingOnEachMembersScale(t *testing.T) {
	d := load(t, shelf(), nil,
		member{"ann", &fileformat.TasteMeta{Scale: &fileformat.Scale{Min: 1, Max: 5}}, []fileformat.TasteItem{{ID: "a", Rating: r(5)}, {ID: "c", Rating: r(1)}}},
		member{"bob", &fileformat.TasteMeta{Scale: &fileformat.Scale{Min: 1, Max: 10, Direction: fileformat.LowerBetter}}, []fileformat.TasteItem{{ID: "c", Rating: r(1)}, {ID: "b", Rating: r(8)}}},
	)
	g, err := group.New(d, 0, 1)
	require.NoError(t, err)
	rating := func(i int) float64 {
		it := shelf()[i]
		res := g.Score(score.Item{ID: it.ID, Facts: it.Facts})
		require.True(t, res.HasRated)
		return res.Rating
	}
	// Raw ratings would order c (1 and 10 after direction, mean 5.5), a (5),
	// b (3); on each member's own scale a is a top rating and comes first.
	assert.InDelta(t, 1.0, rating(0), eps, "ann's 5 is the top of 1-5")
	assert.InDelta(t, 0.5, rating(2), eps, "ann's 1 is 0; bob's 1 on a lower-better scale is 1")
	assert.InDelta(t, 2.0/9, rating(1), eps, "bob's 8 on lower-better 1-10")
}

func TestAffinityIsTheMean(t *testing.T) {
	g, err := group.New(two(t, nil, nil), 0, 1)
	require.NoError(t, err)
	ann, bob := g.Members[0], g.Members[1]
	assert.InDelta(t, (ann.Affinity("tags", "x")+bob.Affinity("tags", "x"))/2, g.Affinity("tags", "x"), eps)
}

func TestRankLeastMiseryTieBreak(t *testing.T) {
	results := []score.Result{
		{ID: "a", Final: 0.5, Floor: 0, Members: []score.MemberScore{{}}},
		{ID: "b", Final: 0.5, Floor: 0.4, Members: []score.MemberScore{{}}},
		{ID: "c", Final: 0.5, Floor: 0.4, HasRated: true, Rating: 6, Members: []score.MemberScore{{}}},
		{ID: "d", Final: 0.5, Floor: 0.4, HasRated: true, Rating: 8, Members: []score.MemberScore{{}}},
	}
	score.Rank(results)
	assert.Equal(t, []string{"d", "c", "b", "a"}, ids(results), "lowest member score highest first, then mean rating (unrated last), then id")
}

func TestPickOverAGroup(t *testing.T) {
	g, err := group.New(two(t, nil, nil), 0, 2)
	require.NoError(t, err)
	s := pick.New(g)
	assert.Equal(t, 4, s.Remaining(), "d does not fit 2")
	q, ok := s.Next()
	require.True(t, ok)
	assert.NotEqual(t, "players", q.Field.Name, "the group size is never asked")
	for i := 1; i < len(q.Options); i++ {
		assert.GreaterOrEqual(t, g.Affinity(q.Field.Name, q.Options[i-1].Key), g.Affinity(q.Field.Name, q.Options[i].Key), "options by mean affinity")
	}
	_, err = s.Apply(pick.Answer{Field: "theme", Key: "sea"})
	require.NoError(t, err)
	res := s.Results()
	require.Equal(t, []string{"a", "b"}, ids(res))
	assert.InDelta(t, res[0].TasteScore+2*1, res[0].Final, eps, "B from the group, the answer matched")
	assert.Contains(t, res[0].Explain(), "a  group score")
	assert.Contains(t, res[0].Explain(), "  ann: ")
	assert.Contains(t, res[0].Explain(), "  matched: theme = sea")
	assert.Contains(t, res[0].Explain(), "  passed: players fits 2, the group size")
}

func TestParseAnswer(t *testing.T) {
	g, err := group.New(two(t, nil, nil), 0, 1)
	require.NoError(t, err)
	a, err := pick.ParseAnswer(g, "theme=sea")
	require.NoError(t, err)
	assert.Equal(t, pick.Answer{Field: "theme", Key: "sea"}, a)
	a, err = pick.ParseAnswer(g, "players=3")
	require.NoError(t, err)
	assert.Equal(t, pick.Answer{Field: "players", Key: "3"}, a)
	a, err = pick.ParseAnswer(g, "theme=")
	require.NoError(t, err)
	assert.Equal(t, pick.NoPreference, a.Kind)
	_, err = pick.ParseAnswer(g, "theme")
	assert.ErrorContains(t, err, "want field=value")
	_, err = pick.ParseAnswer(g, "played=true")
	assert.ErrorContains(t, err, "unknown field", "per-user fields are not answered in group mode")
	_, err = pick.ParseAnswer(g, "players=many")
	assert.ErrorContains(t, err, "want a number")
}

func TestCheckOverAGroup(t *testing.T) {
	acq := []fileformat.Item{
		{ID: "n1", Facts: map[string]schema.Value{"players": fits(2, 4), "tags": set("x"), "theme": cat("sea")}},
		{ID: "n2", Facts: map[string]schema.Value{"players": fits(2, 4), "tags": set("y"), "theme": cat("sea")}},
	}
	d := load(t, shelf(), acq,
		member{"ann", &fileformat.TasteMeta{Blocked: []string{"n2"}}, []fileformat.TasteItem{{ID: "a", Rating: r(10)}, {ID: "c", Rating: r(4)}}},
		member{"bob", nil, []fileformat.TasteItem{{ID: "c", Rating: r(10)}, {ID: "b", Rating: r(4)}}},
	)
	g, err := group.New(d, 0, 1)
	require.NoError(t, err)
	reports := check.Run(g, score.DefaultSimilarityThreshold)
	require.Len(t, reports, 2)
	assert.Equal(t, "n1", reports[0].ID)
	require.Len(t, reports[0].Result.Members, 2, "scored for every member and the group")
	assert.Equal(t, &score.Cause{Kind: score.ByList, Member: "ann"}, reports[1].Excluded, "vetoed, not scored")
}

func TestReportCountsPerLabel(t *testing.T) {
	g, err := group.New(two(t,
		&fileformat.TasteMeta{Blocked: []string{"a", "b", "c", "e"}},
		&fileformat.TasteMeta{Blocked: []string{"d"}},
	), 4, 1)
	require.NoError(t, err)
	s := pick.New(g)
	require.NotNil(t, s.Empty())
	assert.Equal(t, []score.CauseCount{
		{Cause: score.Cause{Kind: score.ByGroupSize, Field: "players", Value: "4"}, Items: 1},
		{Cause: score.Cause{Kind: score.ByList, Member: "ann"}, Items: 3},
		{Cause: score.Cause{Kind: score.ByList, Member: "bob"}, Items: 1},
	}, s.Empty().Causes)
}

func TestMemberContributionCounts(t *testing.T) {
	items := []fileformat.Item{
		{ID: "liked", Facts: map[string]schema.Value{"tags": set("p", "q", "r"), "theme": cat("sea")}},
		{ID: "hated", Facts: map[string]schema.Value{"tags": set("s", "u"), "theme": cat("moon")}},
		{ID: "mixed", Facts: map[string]schema.Value{"tags": set("p", "q", "r", "s", "u"), "theme": cat("moon")}},
	}
	d := load(t, items, nil,
		member{"ann", nil, []fileformat.TasteItem{{ID: "liked", Rating: r(10)}, {ID: "hated", Rating: r(1)}}},
		member{"bob", nil, nil},
	)
	g, err := group.New(d, 0, 1)
	require.NoError(t, err)
	res := g.Score(score.Item{ID: "mixed", Facts: items[2].Facts})
	ann := res.Members[0]
	assert.Len(t, ann.Positive, 2, "top 2 positive per member, of 3")
	assert.Len(t, ann.Negative, 1, "top 1 negative per member, of 3")
	assert.Empty(t, res.Members[1].Positive, "bob has no history")
}
