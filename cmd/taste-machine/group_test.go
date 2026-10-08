package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/fileformat"
)

func writeSecondTaste(t *testing.T, dir string, meta *fileformat.TasteMeta) string {
	t.Helper()
	rating := 2.0
	p := filepath.Join(dir, "second.zip")
	require.NoError(t, fileformat.WriteFile(p, &fileformat.Taste{
		Meta:  fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindTaste, Taste: meta},
		Items: []fileformat.TasteItem{{ID: "b", Rating: &rating}},
	}))
	return p
}

func TestPickGroupOneShot(t *testing.T) {
	dir := t.TempDir()
	sp, tp := writeFiles(t, dir, &fileformat.TasteMeta{Answers: []fileformat.Answer{{Field: "theme", Value: []byte(`"space"`)}}})
	tp2 := writeSecondTaste(t, dir, &fileformat.TasteMeta{Label: "two", Favourites: []string{"c"}})
	te := &testEnv{}
	code := run(context.Background(), []string{"pick", "--shelf", sp, "--taste", tp, "--taste", tp2, "--one-shot", "--answer", "theme=sea", "--answer-weight", "0.5", "--top", "1"}, te.env())
	require.Equal(t, 0, code, te.stderr.String())
	out := te.stdout.String()
	assert.Contains(t, out, "c  group score 1.000\n", "c: two's favourite (1) and taste's 0, mean 0.5, plus B 0.5 × the matched answer 1")
	assert.Contains(t, out, "  taste: 0.000\n  two: 1.000 (favourite)\n")
	assert.Contains(t, out, "  matched: theme = sea\n")
	assert.Contains(t, out, "  favourite of: two\n")
	assert.NotContains(t, out, "space", "stored answers in the taste files are ignored in group mode")
}

func TestPickGroupFlagErrors(t *testing.T) {
	dir := t.TempDir()
	sp, tp := writeFiles(t, dir, nil)
	tp2 := writeSecondTaste(t, dir, nil)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pick", "--shelf", sp, "--taste", tp, "--size", "3"}, "--size and --answer-weight apply to group mode only"},
		{[]string{"pick", "--shelf", sp, "--taste", tp, "--answer-weight", "2"}, "--size and --answer-weight apply to group mode only"},
		{[]string{"pick", "--shelf", sp, "--taste", tp, "--one-shot", "--answer", "theme=sea"}, "--answer needs --one-shot and group mode"},
		{[]string{"pick", "--shelf", sp, "--taste", tp, "--taste", tp2, "--answer", "theme=sea"}, "--answer needs --one-shot and group mode"},
		{[]string{"pick", "--shelf", sp, "--taste", tp, "--taste", tp2, "--one-shot", "--answer", "theme"}, "want field=value"},
		{[]string{"pick", "--shelf", sp, "--taste", tp, "--taste", tp, "--one-shot"}, `label "taste" is used twice`},
	}
	for _, tc := range cases {
		te := &testEnv{}
		assert.Equal(t, 1, run(context.Background(), tc.args, te.env()), tc.args)
		assert.Contains(t, te.stderr.String(), tc.want, tc.args)
	}
}

func TestCheckGroup(t *testing.T) {
	dir := t.TempDir()
	sp, tp := writeFiles(t, dir, nil)
	tp2 := writeSecondTaste(t, dir, &fileformat.TasteMeta{Label: "two", Blocked: []string{"x"}})
	ap := writeAcquisition(t, dir)
	te := &testEnv{}
	code := run(context.Background(), []string{"check", "--shelf", sp, "--taste", tp, "--taste", tp2, "--acquisition", ap}, te.env())
	require.Equal(t, 0, code, te.stderr.String())
	out := te.stdout.String()
	assert.Contains(t, out, "x  vetoed: on the blocked list (two)\n")
	assert.Contains(t, out, "new  group score")
}
