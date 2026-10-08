package fileformat_test

import (
	"archive/zip"
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
)

var update = flag.Bool("update", false, "rewrite golden files")

func shelfSchema() schema.Schema {
	return schema.Schema{Fields: []schema.Field{
		{Name: "fits", Type: schema.Range, Role: schema.Filter, GroupSize: true},
		{Name: "weight", Type: schema.Number, Role: schema.Preference, Buckets: 3},
		{Name: "tags", Type: schema.Set, Role: schema.Both, Askable: true},
		{Name: "best_at", Type: schema.Votes, Role: schema.Preference},
	}}
}

func value(t *testing.T, f schema.Field, raw string) schema.Value {
	t.Helper()
	v, err := f.Decode([]byte(raw))
	require.NoError(t, err)
	return v
}

func sampleCatalogue(t *testing.T, kind fileformat.Kind) *fileformat.Catalogue {
	t.Helper()
	s := shelfSchema()
	fits, _ := s.Field("fits")
	weight, _ := s.Field("weight")
	tags, _ := s.Field("tags")
	best, _ := s.Field("best_at")
	return &fileformat.Catalogue{
		Meta: fileformat.Metadata{FormatVersion: 1, SchemaID: "test/v1", Kind: kind, Schema: s},
		Items: []fileformat.Item{
			{ID: "b", Facts: map[string]schema.Value{
				"fits":    value(t, fits, `{"min":2,"max":4}`),
				"weight":  value(t, weight, `2.5`),
				"tags":    value(t, tags, `["y","x"]`),
				"best_at": value(t, best, `{"3":5,"4":2}`),
			}},
			{ID: "a", Facts: map[string]schema.Value{
				"weight": value(t, weight, `1`),
			}},
		},
	}
}

func rating(r float64) *float64 { return &r }

func sampleTaste(t *testing.T) *fileformat.Taste {
	t.Helper()
	owned := schema.Field{Name: "owned", Type: schema.Bool, Role: schema.Filter}
	return &fileformat.Taste{
		Meta: fileformat.Metadata{
			FormatVersion: 1, SchemaID: "test/v1", Kind: fileformat.KindTaste,
			Schema: schema.Schema{Fields: []schema.Field{owned}},
			Taste: &fileformat.TasteMeta{
				Label:       "first",
				Scale:       &fileformat.Scale{Min: 0, Max: 5, Decimals: true},
				Preferences: map[string]map[string]fileformat.Stance{"tags": {"x": fileformat.Like, "z": fileformat.Never}},
				Blocked:     []string{"a"},
				Favourites:  []string{"b"},
				Constants:   fileformat.Constants{K: rating(2)},
				Answers:     []fileformat.Answer{{Field: "tags", Value: []byte(`"x"`)}},
			},
		},
		Items: []fileformat.TasteItem{
			{ID: "b", Rating: rating(4.5), Plays: 3, Fields: map[string]schema.Value{"owned": value(t, owned, `true`)}},
			{ID: "old", Plays: 1},
		},
		LearnFrom: &fileformat.Catalogue{
			Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "test/v1", Kind: fileformat.KindCatalogue, Schema: shelfSchema()},
			Items: []fileformat.Item{{ID: "old"}},
		},
	}
}

func write(t *testing.T, v interface{ Write(io.Writer) error }) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, v.Write(&buf))
	return buf.Bytes()
}

func TestCatalogueRoundTrip(t *testing.T) {
	for _, kind := range []fileformat.Kind{fileformat.KindCatalogue, fileformat.KindAcquisition} {
		t.Run(string(kind), func(t *testing.T) {
			c := sampleCatalogue(t, kind)
			raw := write(t, c)
			got, err := fileformat.ReadCatalogue(bytes.NewReader(raw), int64(len(raw)))
			require.NoError(t, err)
			assert.Equal(t, c.Meta, got.Meta)
			require.Len(t, got.Items, 2)
			assert.Equal(t, "a", got.Items[0].ID, "items are written in id order")
			assert.Equal(t, c.Items[0], got.Items[1])
			assert.Equal(t, raw, write(t, got), "reading and writing again gives the same bytes")
		})
	}
}

func TestTasteRoundTrip(t *testing.T) {
	tf := sampleTaste(t)
	raw := write(t, tf)
	got, err := fileformat.ReadTaste(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	assert.Equal(t, tf.Meta, got.Meta)
	assert.Equal(t, tf.Items, got.Items)
	require.NotNil(t, got.LearnFrom)
	assert.Equal(t, tf.LearnFrom.Meta, got.LearnFrom.Meta)
	assert.Equal(t, raw, write(t, got))
}

func TestFileHelpers(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "shelf.zip")
	tp := filepath.Join(dir, "taste.zip")
	require.NoError(t, fileformat.WriteFile(cp, sampleCatalogue(t, fileformat.KindCatalogue)))
	require.NoError(t, fileformat.WriteFile(tp, sampleTaste(t)))
	c, err := fileformat.ReadCatalogueFile(cp)
	require.NoError(t, err)
	assert.Len(t, c.Items, 2)
	tf, err := fileformat.ReadTasteFile(tp)
	require.NoError(t, err)
	assert.Len(t, tf.Items, 2)

	_, err = fileformat.ReadTasteFile(cp)
	assert.ErrorContains(t, err, `kind is "catalogue", want "taste"`)
	assert.ErrorContains(t, err, cp)
	_, err = fileformat.ReadCatalogueFile(tp)
	assert.ErrorContains(t, err, `kind is "taste"`)
	_, err = fileformat.ReadCatalogueFile(filepath.Join(dir, "missing.zip"))
	assert.Error(t, err)
}

// TestGolden pins the exact bytes inside a written file, so a change to the
// format shows up as a diff. Run with -update to rewrite the golden files.
func TestGolden(t *testing.T) {
	raw := write(t, sampleTaste(t))
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	for _, name := range []string{"metadata.json", "items.jsonl"} {
		f, err := z.Open(name)
		require.NoError(t, err)
		got, err := io.ReadAll(f)
		require.NoError(t, err)
		golden := filepath.Join("testdata", "taste."+name)
		if *update {
			require.NoError(t, os.WriteFile(golden, got, 0o644))
		}
		want, err := os.ReadFile(golden)
		require.NoError(t, err)
		assert.Equal(t, string(want), string(got), name)
	}
}

// zipOf builds a file from raw entries, to test what the writer never
// produces.
func zipOf(t *testing.T, entries ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		require.NoError(t, err)
		_, err = w.Write([]byte(e[1]))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

const (
	catMeta   = `{"format_version":1,"schema_id":"s","kind":"catalogue","schema":{"fields":[{"name":"tags","type":"set","role":"both"}]}}`
	tasteMeta = `{"format_version":1,"schema_id":"s","kind":"taste","schema":{"fields":[]},"taste":{"scale":{"min":1,"max":5}}}`
)

func TestCatalogueLoadErrors(t *testing.T) {
	cases := []struct {
		name    string
		entries [][2]string
		want    string
	}{
		{"missing metadata", [][2]string{{"items.jsonl", ""}}, "missing metadata.json"},
		{"missing items", [][2]string{{"metadata.json", catMeta}}, "missing items.jsonl"},
		{"format version", [][2]string{{"metadata.json", `{"format_version":2,"schema_id":"s","kind":"catalogue","schema":{"fields":[]}}`}, {"items.jsonl", ""}}, "unsupported format_version 2"},
		{"unknown metadata key", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"catalogue","schema":{"fields":[]},"shema":1}`}, {"items.jsonl", ""}}, `unknown field "shema"`},
		{"unknown kind", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"shelf","schema":{"fields":[]}}`}, {"items.jsonl", ""}}, `unknown kind "shelf"`},
		{"empty schema id", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"","kind":"catalogue","schema":{"fields":[]}}`}, {"items.jsonl", ""}}, "empty schema_id"},
		{"invalid schema", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"catalogue","schema":{"fields":[{"name":"f","type":"votes","role":"filter"}]}}`}, {"items.jsonl", ""}}, "cannot have role filter"},
		{"taste metadata on catalogue", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"catalogue","schema":{"fields":[]},"taste":{}}`}, {"items.jsonl", ""}}, "carries taste metadata"},
		{"duplicate id", [][2]string{{"metadata.json", catMeta}, {"items.jsonl", "{\"id\":\"a\"}\n{\"id\":\"a\"}\n"}}, `line 2: item "a" appears twice`},
		{"empty id", [][2]string{{"metadata.json", catMeta}, {"items.jsonl", `{"facts":{}}`}}, "line 1: empty id"},
		{"undeclared field", [][2]string{{"metadata.json", catMeta}, {"items.jsonl", `{"id":"a","facts":{"colour":"red"}}`}}, `field "colour" is not declared`},
		{"bad value", [][2]string{{"metadata.json", catMeta}, {"items.jsonl", `{"id":"a","facts":{"tags":"x"}}`}}, `field "tags"`},
		{"rating on catalogue", [][2]string{{"metadata.json", catMeta}, {"items.jsonl", `{"id":"a","rating":3}`}}, "belong in a taste file"},
		{"unknown item key", [][2]string{{"metadata.json", catMeta}, {"items.jsonl", `{"id":"a","fact":{}}`}}, `unknown field "fact"`},
		{"bad json line", [][2]string{{"metadata.json", catMeta}, {"items.jsonl", "{\"id\":\"a\"}\n{\n"}}, "items.jsonl line 2"},
		{"learn-from in catalogue", [][2]string{{"metadata.json", catMeta}, {"items.jsonl", ""}, {"learn-from.zip", ""}}, "carries learn-from.zip"},
		{"metadata twice", [][2]string{{"metadata.json", catMeta}, {"metadata.json", catMeta}, {"items.jsonl", ""}}, "metadata.json appears twice"},
		{"taste as catalogue", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", ""}}, `kind is "taste"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := zipOf(t, tc.entries...)
			_, err := fileformat.ReadCatalogue(bytes.NewReader(raw), int64(len(raw)))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func tasteMetaWith(taste string) string {
	return `{"format_version":1,"schema_id":"s","kind":"taste","schema":{"fields":[]},"taste":` + taste + `}`
}

func TestTasteLoadErrors(t *testing.T) {
	lfOther := zipOf(t, [2]string{"metadata.json", `{"format_version":1,"schema_id":"other","kind":"catalogue","schema":{"fields":[]}}`}, [2]string{"items.jsonl", ""})
	lfAcq := zipOf(t, [2]string{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"acquisition","schema":{"fields":[]}}`}, [2]string{"items.jsonl", ""})
	cases := []struct {
		name    string
		entries [][2]string
		want    string
	}{
		{"catalogue as taste", [][2]string{{"metadata.json", catMeta}, {"items.jsonl", ""}}, `kind is "catalogue", want "taste"`},
		{"bucket edges in taste", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"taste","schema":{"fields":[{"name":"n","type":"number","role":"preference","edges":[1,2]}]}}`}, {"items.jsonl", ""}}, "bucket settings are allowed in catalogue metadata only"},
		{"bucket count in taste", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"taste","schema":{"fields":[{"name":"n","type":"number","role":"preference","buckets":3}]}}`}, {"items.jsonl", ""}}, "bucket settings are allowed in catalogue metadata only"},
		{"group_size in taste", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"taste","schema":{"fields":[{"name":"r","type":"range","role":"filter","group_size":true}]}}`}, {"items.jsonl", ""}}, "group_size is allowed in catalogue metadata only"},
		{"display in taste", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"taste","schema":{"fields":[{"name":"c","type":"category","role":"info","display":true}]}}`}, {"items.jsonl", ""}}, "display is allowed in catalogue metadata only"},
		{"blocked and favourites", [][2]string{{"metadata.json", tasteMetaWith(`{"blocked":["a","b"],"favourites":["b"]}`)}, {"items.jsonl", ""}}, `item "b" is on both blocked and favourites`},
		{"listed twice", [][2]string{{"metadata.json", tasteMetaWith(`{"blocked":["a","a"]}`)}, {"items.jsonl", ""}}, `blocked: item "a" listed twice`},
		{"empty list id", [][2]string{{"metadata.json", tasteMetaWith(`{"favourites":[""]}`)}, {"items.jsonl", ""}}, "favourites: empty id"},
		{"unknown stance", [][2]string{{"metadata.json", tasteMetaWith(`{"preferences":{"tags":{"x":"love"}}}`)}, {"items.jsonl", ""}}, `unknown stance "love"`},
		{"scale inverted", [][2]string{{"metadata.json", tasteMetaWith(`{"scale":{"min":5,"max":5}}`)}, {"items.jsonl", ""}}, "min 5 is not below max 5"},
		{"scale direction", [][2]string{{"metadata.json", tasteMetaWith(`{"scale":{"min":1,"max":5,"direction":"up"}}`)}, {"items.jsonl", ""}}, `unknown direction "up"`},
		{"constant k", [][2]string{{"metadata.json", tasteMetaWith(`{"constants":{"k":-1}}`)}, {"items.jsonl", ""}}, "constant k = -1"},
		{"constant play_weight", [][2]string{{"metadata.json", tasteMetaWith(`{"constants":{"play_weight":1.5}}`)}, {"items.jsonl", ""}}, "constant play_weight = 1.5"},
		{"constant play_saturation", [][2]string{{"metadata.json", tasteMetaWith(`{"constants":{"play_saturation":1}}`)}, {"items.jsonl", ""}}, "constant play_saturation = 1"},
		{"constant answer_weight", [][2]string{{"metadata.json", tasteMetaWith(`{"constants":{"answer_weight":-0.5}}`)}, {"items.jsonl", ""}}, "constant answer_weight = -0.5"},
		{"answer without field", [][2]string{{"metadata.json", tasteMetaWith(`{"answers":[{"value":"x"}]}`)}, {"items.jsonl", ""}}, "answer 0: empty field"},
		{"rating above scale", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", `{"id":"a","rating":6}`}}, "rating 6 is outside the scale [1, 5]"},
		{"rating below scale", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", `{"id":"a","rating":0}`}}, "rating 0 is outside the scale [1, 5]"},
		{"rating decimals", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", `{"id":"a","rating":3.5}`}}, "whole numbers only"},
		{"default scale", [][2]string{{"metadata.json", `{"format_version":1,"schema_id":"s","kind":"taste","schema":{"fields":[]}}`}, {"items.jsonl", `{"id":"a","rating":11}`}}, "outside the scale [1, 10]"},
		{"negative plays", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", `{"id":"a","plays":-1}`}}, "negative plays -1"},
		{"facts in taste", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", `{"id":"a","facts":{}}`}}, "facts belong in a catalogue file"},
		{"undeclared per-user field", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", `{"id":"a","fields":{"owned":true}}`}}, `field "owned" is not declared`},
		{"learn-from schema_id", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", ""}, {"learn-from.zip", string(lfOther)}}, `learn-from.zip: schema_id "other" does not match the taste file's "s"`},
		{"learn-from kind", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", ""}, {"learn-from.zip", string(lfAcq)}}, `learn-from.zip: kind is "acquisition"`},
		{"learn-from not a zip", [][2]string{{"metadata.json", tasteMeta}, {"items.jsonl", ""}, {"learn-from.zip", "nope"}}, "learn-from.zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := zipOf(t, tc.entries...)
			_, err := fileformat.ReadTaste(bytes.NewReader(raw), int64(len(raw)))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestTasteAccepts(t *testing.T) {
	raw := zipOf(t,
		[2]string{"metadata.json", tasteMetaWith(`{"scale":{"min":1,"max":5,"direction":"lower","decimals":true}}`)},
		[2]string{"items.jsonl", "{\"id\":\"a\",\"rating\":1.5}\n\n{\"id\":\"b\",\"plays\":0}\n"},
	)
	tf, err := fileformat.ReadTaste(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	require.Len(t, tf.Items, 2)
	assert.Equal(t, fileformat.LowerBetter, tf.Meta.Taste.EffectiveScale().Direction)
	assert.Nil(t, tf.Items[1].Rating)
}

func TestEffectiveScale(t *testing.T) {
	var none *fileformat.TasteMeta
	assert.Equal(t, fileformat.DefaultScale, none.EffectiveScale())
	assert.Equal(t, fileformat.DefaultScale, (&fileformat.TasteMeta{}).EffectiveScale())
	s := (&fileformat.TasteMeta{Scale: &fileformat.Scale{Min: 0, Max: 5}}).EffectiveScale()
	assert.Equal(t, fileformat.HigherBetter, s.Direction)
}

func TestWriteRejectsInvalidMetadata(t *testing.T) {
	c := sampleCatalogue(t, fileformat.KindCatalogue)
	c.Meta.FormatVersion = 0
	var buf bytes.Buffer
	assert.ErrorContains(t, c.Write(&buf), "unsupported format_version 0")
}
