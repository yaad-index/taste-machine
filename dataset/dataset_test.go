package dataset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/dataset"
	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
)

func shelfSchema() schema.Schema {
	return schema.Schema{Fields: []schema.Field{
		{Name: "fits", Type: schema.Range, Role: schema.Filter, GroupSize: true},
		{Name: "weight", Type: schema.Number, Role: schema.Preference},
		{Name: "tags", Type: schema.Set, Role: schema.Both},
		{Name: "theme", Type: schema.Category, Role: schema.Preference},
		{Name: "solo", Type: schema.Bool, Role: schema.Filter},
		{Name: "note", Type: schema.Category, Role: schema.Info},
	}}
}

func setVal(vals ...string) schema.Value { return schema.Value{Type: schema.Set, Set: vals} }

func catalogue(kind fileformat.Kind, items ...fileformat.Item) *fileformat.Catalogue {
	return &fileformat.Catalogue{
		Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: kind, Schema: shelfSchema()},
		Items: items,
	}
}

func shelf() *fileformat.Catalogue {
	return catalogue(fileformat.KindCatalogue,
		fileformat.Item{ID: "c", Facts: map[string]schema.Value{"tags": setVal("x")}},
		fileformat.Item{ID: "a", Facts: map[string]schema.Value{"tags": setVal("y")}},
	)
}

func taste(meta *fileformat.TasteMeta, items ...fileformat.TasteItem) *fileformat.Taste {
	return &fileformat.Taste{
		Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindTaste, Taste: meta},
		Items: items,
	}
}

func r(v float64) *float64 { return &v }

func one(tf *fileformat.Taste) []dataset.Input { return []dataset.Input{{Taste: tf, Name: "me"}} }

func TestLoadJoinsAndReports(t *testing.T) {
	lf := catalogue(fileformat.KindCatalogue,
		fileformat.Item{ID: "a", Facts: map[string]schema.Value{"tags": setVal("from-learn-from")}},
		fileformat.Item{ID: "gone", Facts: map[string]schema.Value{"tags": setVal("z")}},
	)
	tf := taste(&fileformat.TasteMeta{
		Blocked:    []string{"nowhere", "a"},
		Favourites: []string{"new", "also-nowhere"},
	},
		fileformat.TasteItem{ID: "a", Rating: r(8)},
		fileformat.TasteItem{ID: "gone", Rating: r(9)},
		fileformat.TasteItem{ID: "unknown", Plays: 2},
	)
	tf.LearnFrom = lf
	acq := catalogue(fileformat.KindAcquisition,
		fileformat.Item{ID: "new"},
		fileformat.Item{ID: "c"},
	)

	d, err := dataset.Load(shelf(), one(tf), acq)
	require.NoError(t, err)

	assert.Equal(t, []string{"a", "c"}, []string{d.Shelf[0].ID, d.Shelf[1].ID}, "shelf sorted by id")
	assert.Equal(t, []string{"c", "new"}, []string{d.Acquisition[0].ID, d.Acquisition[1].ID})
	require.Len(t, d.Members, 1)
	m := d.Members[0]
	assert.Equal(t, "me", m.Label, "label falls back to the given name")
	assert.Equal(t, []string{"a", "gone"}, m.IDs(), "an id in neither shelf nor learn-from is dropped")

	facts, ok := d.Facts(m, "a")
	require.True(t, ok)
	assert.Equal(t, setVal("y"), facts["tags"], "the shelf's facts win over the learn-from catalogue's")
	facts, ok = d.Facts(m, "gone")
	require.True(t, ok)
	assert.Equal(t, setVal("z"), facts["tags"], "an item off the shelf is learned from the learn-from catalogue")
	_, ok = d.Facts(m, "unknown")
	assert.False(t, ok)

	assert.Equal(t, map[string]bool{"a": true}, m.Blocked)
	assert.Equal(t, map[string]bool{"new": true}, m.Favourites, "list ids resolve against the acquisition list too")

	assert.Equal(t, []dataset.Notice{
		{Kind: dataset.AlreadyInCatalogue, ID: "c"},
		{Kind: dataset.UnknownItem, Member: "me", ID: "unknown"},
		{Kind: dataset.UnknownListItem, Member: "me", ID: "also-nowhere"},
		{Kind: dataset.UnknownListItem, Member: "me", ID: "nowhere"},
	}, d.Notices)
}

func TestLoadMembersSortedByLabel(t *testing.T) {
	d, err := dataset.Load(shelf(), []dataset.Input{
		{Taste: taste(&fileformat.TasteMeta{Label: "zed"}), Name: "1.zip"},
		{Taste: taste(nil), Name: "beta"},
		{Taste: taste(&fileformat.TasteMeta{Label: "alpha"}), Name: "3.zip"},
	}, nil)
	require.NoError(t, err)
	var labels []string
	for _, m := range d.Members {
		labels = append(labels, m.Label)
	}
	assert.Equal(t, []string{"alpha", "beta", "zed"}, labels)
}

func TestLoadErrors(t *testing.T) {
	otherShelf := catalogue(fileformat.KindCatalogue)
	otherShelf.Meta.SchemaID = "other"
	changed := shelfSchema()
	changed.Fields[1].Weight = 2

	cases := []struct {
		name  string
		shelf *fileformat.Catalogue
		in    []dataset.Input
		acq   *fileformat.Catalogue
		want  string
	}{
		{"no shelf", nil, one(taste(nil)), nil, "no shelf"},
		{"shelf of the wrong kind", catalogue(fileformat.KindAcquisition), one(taste(nil)), nil, `shelf: kind is "acquisition"`},
		{"no taste", shelf(), nil, nil, "no taste file"},
		{"taste schema_id", otherShelf, one(taste(nil)), nil, `schema_id "s" does not match the shelf's "other"`},
		{"acquisition schema_id", shelf(), one(taste(nil)), func() *fileformat.Catalogue {
			c := catalogue(fileformat.KindAcquisition)
			c.Meta.SchemaID = "other"
			return c
		}(), `acquisition list: schema_id "other" does not match the shelf's "s"`},
		{"acquisition kind", shelf(), one(taste(nil)), catalogue(fileformat.KindCatalogue), `acquisition list: kind is "catalogue"`},
		{"acquisition schema differs", shelf(), one(taste(nil)), func() *fileformat.Catalogue {
			c := catalogue(fileformat.KindAcquisition)
			c.Meta.Schema = changed
			return c
		}(), "acquisition list: schema_id \"s\" matches the shelf's but the declared fields differ"},
		{"learn-from schema differs", shelf(), one(func() *fileformat.Taste {
			tf := taste(nil)
			tf.LearnFrom = catalogue(fileformat.KindCatalogue)
			tf.LearnFrom.Meta.Schema = changed
			return tf
		}()), nil, "learn-from catalogue: schema_id \"s\" matches the shelf's but the declared fields differ"},
		{"learn-from schema_id", shelf(), one(func() *fileformat.Taste {
			tf := taste(nil)
			tf.LearnFrom = catalogue(fileformat.KindCatalogue)
			tf.LearnFrom.Meta.SchemaID = "other"
			return tf
		}()), nil, `learn-from catalogue: schema_id "other" does not match the shelf's "s"`},
		{"never on a preference-only field", shelf(), one(taste(&fileformat.TasteMeta{
			Preferences: map[string]map[string]fileformat.Stance{"theme": {"space": fileformat.Never}},
		})), nil, `never needs a field whose role includes filter, "theme" has role preference`},
		{"like on a filter-only field", shelf(), one(taste(&fileformat.TasteMeta{
			Preferences: map[string]map[string]fileformat.Stance{"solo": {"true": fileformat.Like}},
		})), nil, `like and dislike need a field whose role includes preference, "solo" has role filter`},
		{"bool preference value", shelf(), one(taste(&fileformat.TasteMeta{
			Preferences: map[string]map[string]fileformat.Stance{"solo": {"yes": fileformat.Never}},
		})), nil, "a bool field takes true or false"},
		{"preference on a number field", shelf(), one(taste(&fileformat.TasteMeta{
			Preferences: map[string]map[string]fileformat.Stance{"weight": {"3": fileformat.Like}},
		})), nil, "a number field has no values to prefer"},
		{"preference on an undeclared field", shelf(), one(taste(&fileformat.TasteMeta{
			Preferences: map[string]map[string]fileformat.Stance{"colour": {"red": fileformat.Like}},
		})), nil, `preference on "colour": field is not declared`},
		{"answer on an undeclared field", shelf(), one(taste(&fileformat.TasteMeta{
			Answers: []fileformat.Answer{{Field: "colour", Value: []byte(`"red"`)}},
		})), nil, `answer 1: field "colour" is not declared`},
		{"per-user field named like a catalogue field", shelf(), one(func() *fileformat.Taste {
			tf := taste(nil)
			tf.Meta.Schema = schema.Schema{Fields: []schema.Field{{Name: "tags", Type: schema.Bool, Role: schema.Filter}}}
			return tf
		}()), nil, `per-user field "tags" has the name of a catalogue field`},
		{"no label", shelf(), []dataset.Input{{Taste: taste(nil)}}, nil, "no label"},
		{"label used twice", shelf(), []dataset.Input{
			{Taste: taste(&fileformat.TasteMeta{Label: "x"}), Name: "1"},
			{Taste: taste(nil), Name: "x"},
		}, nil, `label "x" is used twice`},
		{"taste of the wrong kind", shelf(), []dataset.Input{{Taste: &fileformat.Taste{Meta: fileformat.Metadata{SchemaID: "s", Kind: fileformat.KindCatalogue}}, Name: "x"}}, nil, `kind is "catalogue", want "taste"`},
		{"nil taste", shelf(), []dataset.Input{{Name: "x"}}, nil, "no taste"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := dataset.Load(tc.shelf, tc.in, tc.acq)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestLoadAcceptsPreferences(t *testing.T) {
	tf := taste(&fileformat.TasteMeta{
		Preferences: map[string]map[string]fileformat.Stance{
			"tags":  {"x": fileformat.Never, "y": fileformat.Like},
			"theme": {"space": fileformat.Dislike},
			"solo":  {"false": fileformat.Never},
		},
		Answers: []fileformat.Answer{{Field: "tags", Value: []byte(`"x"`)}},
	})
	_, err := dataset.Load(shelf(), one(tf), nil)
	require.NoError(t, err)
}

func TestLoadReportsEveryMemberError(t *testing.T) {
	_, err := dataset.Load(shelf(), []dataset.Input{
		{Taste: taste(&fileformat.TasteMeta{Answers: []fileformat.Answer{{Field: "nope"}}}), Name: "one"},
		{Taste: taste(&fileformat.TasteMeta{Preferences: map[string]map[string]fileformat.Stance{"nope": {"x": fileformat.Like}}}), Name: "two"},
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "taste file 1 (one)")
	assert.Contains(t, err.Error(), "taste file 2 (two)")
}
