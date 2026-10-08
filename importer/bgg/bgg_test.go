package bgg_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fzerorubigd/bggo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/dataset"
	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/importer/bgg"
	"github.com/yaad-index/taste-machine/schema"
)

type countingLimiter struct {
	mu    sync.Mutex
	takes int
}

func (l *countingLimiter) Take() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.takes++
	return time.Now()
}

type fakeServer struct {
	t        *testing.T
	mu       sync.Mutex
	requests []*url.URL
	auth     []string
	status   int
}

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.URL)
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	s.mu.Unlock()
	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	var file string
	switch r.URL.Path {
	case "/xmlapi2/collection":
		file = "testdata/collection.xml"
	case "/xmlapi2/thing":
		file = "testdata/things.xml"
	default:
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(file)
	if !assert.NoError(s.t, err) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(raw)
}

func client(t *testing.T, srv *fakeServer, lim bggo.Limiter) *bggo.Client {
	t.Helper()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	return bggo.NewClient("test-key", bggo.WithHost(u.Host), bggo.WithScheme("http"), bggo.WithLimiter(lim))
}

func itemIDs(items []fileformat.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestCompileThroughClient(t *testing.T) {
	srv := &fakeServer{t: t}
	lim := &countingLimiter{}
	im := &bgg.Importer{Source: client(t, srv, lim), Username: "someone"}

	shelf, taste, err := im.Compile(context.Background())
	require.NoError(t, err)

	require.Len(t, srv.requests, 2)
	q := srv.requests[0].Query()
	assert.Equal(t, "/xmlapi2/collection", srv.requests[0].Path)
	assert.Equal(t, "someone", q.Get("username"))
	assert.Equal(t, "boardgame", q.Get("subtype"))
	assert.Equal(t, "boardgameexpansion", q.Get("excludesubtype"))
	assert.Equal(t, "1", q.Get("stats"))
	assert.Equal(t, "/xmlapi2/thing", srv.requests[1].Path)
	assert.Equal(t, "101,102,201", srv.requests[1].Query().Get("id"), "owned, rated and played ids, not the wishlist-only one")
	assert.Equal(t, []string{"Bearer test-key", "Bearer test-key"}, srv.auth)
	assert.Equal(t, 2, lim.takes, "every request goes through the limiter")

	assert.Equal(t, []string{"101", "102"}, itemIDs(shelf.Items), "the shelf is the owned items")
	require.NotNil(t, taste.LearnFrom)
	assert.Equal(t, []string{"201"}, itemIDs(taste.LearnFrom.Items), "rated or played but not owned items are learned from")

	want := map[string]schema.Value{
		bgg.FieldPlayers:      {Type: schema.Range, Range: schema.RangeValue{Min: 2, Max: 4}},
		bgg.FieldBestPlayers:  {Type: schema.Votes, Votes: map[string]float64{"2": 2, "3": 20}},
		bgg.FieldWeight:       {Type: schema.Number, Number: 2.75},
		bgg.FieldPlayingTime:  {Type: schema.Number, Number: 60},
		bgg.FieldMinAge:       {Type: schema.Number, Number: 12},
		bgg.FieldMechanics:    {Type: schema.Set, Set: []string{"Mechanic X"}},
		bgg.FieldCategories:   {Type: schema.Set, Set: []string{"Category A", "Category B"}},
		bgg.FieldDesigners:    {Type: schema.Set, Set: []string{"Designer One"}},
		bgg.FieldName:         {Type: schema.Category, Category: "Game One"},
		bgg.FieldYear:         {Type: schema.Number, Number: 2001},
		bgg.FieldAverage:      {Type: schema.Number, Number: 7.5},
		bgg.FieldBayesAverage: {Type: schema.Number, Number: 7.1},
		bgg.FieldUsersRated:   {Type: schema.Number, Number: 1500},
		bgg.FieldRank:         {Type: schema.Number, Number: 250},
	}
	assert.Equal(t, want, shelf.Items[0].Facts)
	assert.Equal(t, map[string]schema.Value{
		bgg.FieldPlayers: {Type: schema.Range, Range: schema.RangeValue{Min: 1, Max: 1}},
		bgg.FieldName:    {Type: schema.Category, Category: "Game Two"},
	}, shelf.Items[1].Facts, "zero and unranked values are left missing, not written as 0")

	played := func(b bool) map[string]schema.Value {
		return map[string]schema.Value{bgg.FieldPlayed: {Type: schema.Bool, Bool: b}}
	}
	r := func(v float64) *float64 { return &v }
	assert.Equal(t, []fileformat.TasteItem{
		{ID: "101", Rating: r(8.5), Plays: 5, Fields: played(true)},
		{ID: "102", Fields: played(false)},
		{ID: "201", Rating: r(4), Plays: 1, Fields: played(true)},
	}, taste.Items, "duplicate collection rows merge: owned if any row owns it, the most plays, the first rating set")
	assert.Equal(t, "someone", taste.Meta.Taste.Label)
	assert.Equal(t, bgg.Scale, *taste.Meta.Taste.Scale)

	d, err := dataset.Load(roundTrip(t, shelf), []dataset.Input{{Taste: roundTripTaste(t, taste), Name: "x"}}, nil)
	require.NoError(t, err, "the importer's files pass load validation")
	assert.Empty(t, d.Notices)
}

func roundTrip(t *testing.T, c *fileformat.Catalogue) *fileformat.Catalogue {
	t.Helper()
	var buf strings.Builder
	require.NoError(t, c.Write(&buf))
	raw := buf.String()
	got, err := fileformat.ReadCatalogue(strings.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	return got
}

func roundTripTaste(t *testing.T, tf *fileformat.Taste) *fileformat.Taste {
	t.Helper()
	var buf strings.Builder
	require.NoError(t, tf.Write(&buf))
	raw := buf.String()
	got, err := fileformat.ReadTaste(strings.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	return got
}

func TestCompileHTTPError(t *testing.T) {
	srv := &fakeServer{t: t, status: http.StatusTooManyRequests}
	im := &bgg.Importer{Source: client(t, srv, &countingLimiter{}), Username: "someone"}
	_, _, err := im.Compile(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collection")
	var statusErr *bggo.HTTPStatusError
	assert.ErrorAs(t, err, &statusErr, "the client's status error is kept, not swallowed")
	assert.Len(t, srv.requests, 1, "the importer adds no retry of its own")
}

type fakeSource struct {
	coll    []bggo.CollectionItem
	batches [][]int64
	err     error
}

func (f *fakeSource) GetCollection(context.Context, bggo.GetCollectionRequest) ([]bggo.CollectionItem, error) {
	return f.coll, nil
}

func (f *fakeSource) GetThings(_ context.Context, req bggo.GetThingsRequest) ([]bggo.ThingResult, error) {
	f.batches = append(f.batches, req.IDs)
	if f.err != nil {
		return nil, f.err
	}
	var out []bggo.ThingResult
	for _, id := range req.IDs {
		out = append(out, bggo.ThingResult{ID: id, Name: "n" + strconv.FormatInt(id, 10)})
	}
	return out, nil
}

func owned(n int) []bggo.CollectionItem {
	var out []bggo.CollectionItem
	for i := n; i >= 1; i-- {
		out = append(out, bggo.CollectionItem{ID: int64(i), Status: []bggo.CollectionStatus{bggo.CollectionOwn}})
	}
	return out
}

func TestCompileBatchesThings(t *testing.T) {
	src := &fakeSource{coll: owned(45)}
	shelf, taste, err := (&bgg.Importer{Source: src, Username: "u", Label: "me"}).Compile(context.Background())
	require.NoError(t, err)
	require.Len(t, src.batches, 3)
	assert.Len(t, src.batches[0], 20)
	assert.Len(t, src.batches[1], 20)
	assert.Equal(t, []int64{41, 42, 43, 44, 45}, src.batches[2], "ids are fetched in ascending order")
	assert.Len(t, shelf.Items, 45)
	assert.Nil(t, taste.LearnFrom, "nothing outside the shelf, no learn-from catalogue")
	assert.Equal(t, "me", taste.Meta.Taste.Label)
}

func TestCompileErrors(t *testing.T) {
	_, _, err := (&bgg.Importer{Source: &fakeSource{}}).Compile(context.Background())
	assert.ErrorContains(t, err, "no username")

	src := &fakeSource{coll: owned(2), err: errors.New("boom")}
	_, _, err = (&bgg.Importer{Source: src, Username: "u"}).Compile(context.Background())
	assert.ErrorContains(t, err, "things 1 to 2: boom")
}

func TestFactsVotesSumRepeatedCounts(t *testing.T) {
	f := bgg.Facts(&bggo.ThingResult{SuggestedPlayerCount: []bggo.SuggestedPlayerCount{
		{NumPlayers: "4+", Best: 1}, {NumPlayers: "4+", Best: 2}, {NumPlayers: "", Best: 5}, {NumPlayers: "2", Best: 0},
	}})
	assert.Equal(t, map[string]float64{"4+": 3}, f[bgg.FieldBestPlayers].Votes)
	assert.NotContains(t, bgg.Facts(&bggo.ThingResult{MinPlayers: 3, MaxPlayers: 2}), bgg.FieldPlayers, "an inverted player range is left missing")
}

func TestSchemasValidate(t *testing.T) {
	require.NoError(t, bgg.CatalogueSchema().Validate())
	require.NoError(t, bgg.TasteSchema().Validate())
}

func TestLimiterSpacesRequests(t *testing.T) {
	l := &bgg.Limiter{Interval: 30 * time.Millisecond}
	start := time.Now()
	for range 3 {
		l.Take()
	}
	assert.GreaterOrEqual(t, time.Since(start), 60*time.Millisecond)
}
