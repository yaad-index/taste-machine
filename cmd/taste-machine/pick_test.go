package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
)

// writeFiles writes a shelf of nine items and a taste file into dir.
func writeFiles(t *testing.T, dir string, meta *fileformat.TasteMeta) (shelfPath, tastePath string) {
	t.Helper()
	s := schema.Schema{Fields: []schema.Field{
		{Name: "theme", Type: schema.Category, Role: schema.Preference, Askable: true},
		{Name: "solo", Type: schema.Bool, Role: schema.Filter, Askable: true},
	}}
	shelf := &fileformat.Catalogue{Meta: fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindCatalogue, Schema: s}}
	for i, theme := range []string{"sea", "sea", "sea", "sea", "space", "space", "space", "space", "forest"} {
		shelf.Items = append(shelf.Items, fileformat.Item{ID: string(rune('a' + i)), Facts: map[string]schema.Value{
			"theme": {Type: schema.Category, Category: theme},
			"solo":  {Type: schema.Bool, Bool: i == 0},
		}})
	}
	rating := 9.0
	taste := &fileformat.Taste{
		Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindTaste, Taste: meta},
		Items: []fileformat.TasteItem{{ID: "b", Rating: &rating}, {ID: "zz", Plays: 1}},
	}
	shelfPath, tastePath = filepath.Join(dir, "shelf.zip"), filepath.Join(dir, "taste.zip")
	require.NoError(t, fileformat.WriteFile(shelfPath, shelf))
	require.NoError(t, fileformat.WriteFile(tastePath, taste))
	return shelfPath, tastePath
}

func TestPickInteractive(t *testing.T) {
	sp, tp := writeFiles(t, t.TempDir(), nil)
	te := &testEnv{stdin: "x\n9\n1\n"}
	code := run(context.Background(), []string{"pick", "--shelf", sp, "--taste", tp}, te.env())
	require.Equal(t, 0, code, te.stderr.String())
	out := te.stdout.String()
	assert.Contains(t, out, "theme? (9 left)\n  1) sea (4)\n  2) space (4)\n  3) forest (1)\n  o) other\n  n) no preference\n  s) stop and show the results\n")
	assert.Contains(t, out, "solo? (4 left)", "after sea, solo splits the four sea items; invalid input was asked again")
	assert.Contains(t, te.stderr.String(), "note: unknown item zz, ignored")
}

func TestPickInteractiveUndo(t *testing.T) {
	sp, tp := writeFiles(t, t.TempDir(), nil)
	te := &testEnv{stdin: "1\no\ny\n"}
	code := run(context.Background(), []string{"pick", "--shelf", sp, "--taste", tp}, te.env())
	require.Equal(t, 0, code, te.stderr.String())
	out := te.stdout.String()
	assert.Contains(t, out, "No items left:\n  4 removed: does not match the answer to solo (none of false, true)\nUndo that answer? [Y/n]\n")
	assert.Contains(t, out, "b  score", "after the undo, the sea items are shown")
}

func TestPickInteractiveKeepEmpty(t *testing.T) {
	sp, tp := writeFiles(t, t.TempDir(), nil)
	te := &testEnv{stdin: "1\no\nn\n"}
	assert.Equal(t, exitEmpty, run(context.Background(), []string{"pick", "--shelf", sp, "--taste", tp}, te.env()))
}

func TestPickOneShot(t *testing.T) {
	sp, tp := writeFiles(t, t.TempDir(), &fileformat.TasteMeta{Answers: []fileformat.Answer{
		{Field: "theme", Value: []byte(`"sea"`)},
		{Field: "solo", Value: []byte(`null`)},
	}})
	te := &testEnv{}
	code := run(context.Background(), []string{"pick", "--shelf", sp, "--taste", tp, "--one-shot", "--top", "2"}, te.env())
	require.Equal(t, 0, code, te.stderr.String())
	out := te.stdout.String()
	assert.NotContains(t, out, "?", "one-shot asks nothing")
	assert.Contains(t, out, "b  score")
	assert.Contains(t, out, "matched: theme = sea")
	assert.Equal(t, 2, strings.Count(out, "  score "), "--top limits the results")
}

func TestPickOneShotEmpty(t *testing.T) {
	sp, tp := writeFiles(t, t.TempDir(), &fileformat.TasteMeta{Answers: []fileformat.Answer{
		{Field: "theme", Value: []byte(`"space"`)},
		{Field: "solo", Value: []byte(`"true"`)},
	}})
	te := &testEnv{}
	assert.Equal(t, exitEmpty, run(context.Background(), []string{"pick", "--shelf", sp, "--taste", tp, "--one-shot"}, te.env()))
	assert.Contains(t, te.stdout.String(), "4 removed: does not match the answer to solo (true)")
}

func TestPickEmptyFromFilters(t *testing.T) {
	sp, tp := writeFiles(t, t.TempDir(), &fileformat.TasteMeta{Blocked: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}})
	te := &testEnv{}
	assert.Equal(t, exitEmpty, run(context.Background(), []string{"pick", "--shelf", sp, "--taste", tp}, te.env()))
	assert.Contains(t, te.stdout.String(), "9 removed: on the blocked list")
}

func TestPickBadStoredAnswer(t *testing.T) {
	sp, tp := writeFiles(t, t.TempDir(), &fileformat.TasteMeta{Answers: []fileformat.Answer{{Field: "theme", Value: []byte(`3`)}}})
	te := &testEnv{}
	assert.Equal(t, 1, run(context.Background(), []string{"pick", "--shelf", sp, "--taste", tp, "--one-shot"}, te.env()))
	assert.Contains(t, te.stderr.String(), "want a string")
}
