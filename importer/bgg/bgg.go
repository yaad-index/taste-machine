// Package bgg is the board game importer: it compiles one user's board game
// collection into a shelf and a taste file.
package bgg

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/fzerorubigd/bggo"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
)

// SchemaID identifies the schema this importer writes.
const SchemaID = "bgg-boardgame/v1"

// thingBatch is the most ids the thing endpoint takes per request.
const thingBatch = 20

// Catalogue field names.
const (
	FieldPlayers      = "players"
	FieldBestPlayers  = "best_players"
	FieldWeight       = "weight"
	FieldPlayingTime  = "playing_time"
	FieldMinAge       = "min_age"
	FieldMechanics    = "mechanics"
	FieldCategories   = "categories"
	FieldDesigners    = "designers"
	FieldName         = "name"
	FieldYear         = "year"
	FieldAverage      = "average"
	FieldBayesAverage = "bayes_average"
	FieldUsersRated   = "users_rated"
	FieldRank         = "rank"
)

// FieldPlayed is the per-user field in the taste file.
const FieldPlayed = "played"

// CatalogueSchema is the schema of the shelf and the learn-from catalogue.
func CatalogueSchema() schema.Schema {
	return schema.Schema{Fields: []schema.Field{
		{Name: FieldPlayers, Type: schema.Range, Role: schema.Filter, GroupSize: true},
		{Name: FieldBestPlayers, Type: schema.Votes, Role: schema.Preference, Askable: true},
		{Name: FieldWeight, Type: schema.Number, Role: schema.Preference, Askable: true},
		{Name: FieldPlayingTime, Type: schema.Number, Role: schema.Both, Askable: true},
		{Name: FieldMinAge, Type: schema.Number, Role: schema.Filter},
		{Name: FieldMechanics, Type: schema.Set, Role: schema.Both, Askable: true},
		{Name: FieldCategories, Type: schema.Set, Role: schema.Both, Askable: true},
		{Name: FieldDesigners, Type: schema.Set, Role: schema.Preference},
		{Name: FieldName, Type: schema.Category, Role: schema.Info},
		{Name: FieldYear, Type: schema.Number, Role: schema.Info},
		{Name: FieldAverage, Type: schema.Number, Role: schema.Info},
		{Name: FieldBayesAverage, Type: schema.Number, Role: schema.Info},
		{Name: FieldUsersRated, Type: schema.Number, Role: schema.Info},
		{Name: FieldRank, Type: schema.Number, Role: schema.Info},
	}}
}

// TasteSchema declares the per-user fields of the taste file.
func TasteSchema() schema.Schema {
	return schema.Schema{Fields: []schema.Field{
		{Name: FieldPlayed, Type: schema.Bool, Role: schema.Filter},
	}}
}

// Scale is the rating scale the source uses.
var Scale = fileformat.Scale{Min: 1, Max: 10, Direction: fileformat.HigherBetter, Decimals: true}

// Source is the part of the client the importer uses.
type Source interface {
	GetCollection(ctx context.Context, req bggo.GetCollectionRequest) ([]bggo.CollectionItem, error)
	GetThings(ctx context.Context, req bggo.GetThingsRequest) ([]bggo.ThingResult, error)
}

// Importer compiles one user's collection.
type Importer struct {
	Source   Source
	Username string
	// Label is written as the taste file's label. It defaults to Username.
	Label string

	// NoDetails lists, after Compile, the ids the source returned no
	// details for, in id order. They are written without facts.
	NoDetails []string
}

type entry struct {
	owned  bool
	rating *float64
	plays  int
}

// Compile fetches the collection and the facts of every owned, rated or
// played item. The shelf holds the owned items; the taste file holds every
// rated or played item plus an entry per owned item, so the played filter
// sees every shelf item. Facts of rated or played items that are not owned
// go into the taste file's learn-from catalogue.
func (im *Importer) Compile(ctx context.Context) (*fileformat.Catalogue, *fileformat.Taste, error) {
	if im.Username == "" {
		return nil, nil, fmt.Errorf("no username")
	}
	coll, err := im.Source.GetCollection(ctx, bggo.GetCollectionRequest{
		Username:       im.Username,
		SubType:        bggo.BoardGameType,
		ExcludeSubType: bggo.BoardGameExpansionType,
		Stats:          true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("collection: %w", err)
	}
	entries := map[int64]*entry{}
	for _, it := range coll {
		e := entries[it.ID]
		if e == nil {
			e = &entry{}
			entries[it.ID] = e
		}
		e.owned = e.owned || slices.Contains(it.Status, bggo.CollectionOwn)
		if e.rating == nil && it.Rating != nil {
			r := *it.Rating
			e.rating = &r
		}
		e.plays = max(e.plays, it.NumPlays)
	}
	var ids []int64
	for id, e := range entries {
		if e.owned || e.rating != nil || e.plays > 0 {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	facts, err := im.things(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	im.NoDetails = nil
	for _, id := range ids {
		if _, ok := facts[id]; !ok {
			im.NoDetails = append(im.NoDetails, strconv.FormatInt(id, 10))
		}
	}

	shelf := &fileformat.Catalogue{Meta: fileformat.Metadata{
		FormatVersion: fileformat.FormatVersion, SchemaID: SchemaID, Kind: fileformat.KindCatalogue, Schema: CatalogueSchema(),
	}}
	learn := &fileformat.Catalogue{Meta: shelf.Meta}
	label := cmp.Or(im.Label, im.Username)
	scale := Scale
	taste := &fileformat.Taste{Meta: fileformat.Metadata{
		FormatVersion: fileformat.FormatVersion, SchemaID: SchemaID, Kind: fileformat.KindTaste, Schema: TasteSchema(),
		Taste: &fileformat.TasteMeta{Label: label, Scale: &scale},
	}}
	for _, id := range ids {
		e := entries[id]
		key := strconv.FormatInt(id, 10)
		item := fileformat.Item{ID: key, Facts: facts[id]}
		if e.owned {
			shelf.Items = append(shelf.Items, item)
		} else {
			learn.Items = append(learn.Items, item)
		}
		taste.Items = append(taste.Items, fileformat.TasteItem{
			ID:     key,
			Rating: e.rating,
			Plays:  e.plays,
			Fields: map[string]schema.Value{FieldPlayed: {Type: schema.Bool, Bool: e.plays > 0}},
		})
	}
	if len(learn.Items) > 0 {
		taste.LearnFrom = learn
	}
	return shelf, taste, nil
}

// things fetches facts in batches, in id order.
func (im *Importer) things(ctx context.Context, ids []int64) (map[int64]map[string]schema.Value, error) {
	out := make(map[int64]map[string]schema.Value, len(ids))
	for batch := range slices.Chunk(ids, thingBatch) {
		res, err := im.Source.GetThings(ctx, bggo.GetThingsRequest{IDs: batch})
		if err != nil {
			return nil, fmt.Errorf("things %d to %d: %w", batch[0], batch[len(batch)-1], err)
		}
		for i := range res {
			out[res[i].ID] = Facts(&res[i])
		}
	}
	return out, nil
}

// Facts maps one thing to catalogue facts. A value the source leaves empty
// or at zero is omitted, so the engine treats it as missing rather than as
// a real zero.
func Facts(t *bggo.ThingResult) map[string]schema.Value {
	f := map[string]schema.Value{}
	num := func(name string, v float64) {
		if v != 0 {
			f[name] = schema.Value{Type: schema.Number, Number: v}
		}
	}
	set := func(name string, links []bggo.Link) {
		var names []string
		for _, l := range links {
			if l.Name != "" && !slices.Contains(names, l.Name) {
				names = append(names, l.Name)
			}
		}
		if len(names) > 0 {
			slices.Sort(names)
			f[name] = schema.Value{Type: schema.Set, Set: names}
		}
	}
	if t.MinPlayers > 0 && t.MaxPlayers >= t.MinPlayers {
		f[FieldPlayers] = schema.Value{Type: schema.Range, Range: schema.RangeValue{Min: float64(t.MinPlayers), Max: float64(t.MaxPlayers)}}
	}
	votes := map[string]float64{}
	for _, s := range t.SuggestedPlayerCount {
		if s.Best > 0 && s.NumPlayers != "" {
			votes[s.NumPlayers] += float64(s.Best)
		}
	}
	if len(votes) > 0 {
		f[FieldBestPlayers] = schema.Value{Type: schema.Votes, Votes: votes}
	}
	num(FieldWeight, t.AverageWeight)
	num(FieldPlayingTime, float64(t.PlayingTime))
	num(FieldMinAge, float64(t.MinAge))
	set(FieldMechanics, t.Mechanics())
	set(FieldCategories, t.Categories())
	set(FieldDesigners, t.Designers())
	if t.Name != "" {
		f[FieldName] = schema.Value{Type: schema.Category, Category: t.Name}
	}
	num(FieldYear, float64(t.YearPublished))
	num(FieldAverage, t.AverageRate)
	num(FieldBayesAverage, t.BayesAverage)
	num(FieldUsersRated, float64(t.UsersRated))
	num(FieldRank, float64(t.Rank))
	return f
}

// Limiter spaces requests at least Interval apart. It satisfies the
// client's limiter interface.
type Limiter struct {
	Interval time.Duration

	mu   sync.Mutex
	next time.Time
}

// Take blocks until the next request may go out.
func (l *Limiter) Take() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if wait := l.next.Sub(now); wait > 0 {
		time.Sleep(wait)
		now = now.Add(wait)
	}
	l.next = now.Add(l.Interval)
	return now
}
