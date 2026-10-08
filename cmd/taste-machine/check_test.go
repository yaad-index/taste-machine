package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/schema"
)

func writeAcquisition(t *testing.T, dir string) string {
	t.Helper()
	s := schema.Schema{Fields: []schema.Field{
		{Name: "theme", Type: schema.Category, Role: schema.Preference, Askable: true},
		{Name: "solo", Type: schema.Bool, Role: schema.Filter, Askable: true},
	}}
	acq := &fileformat.Catalogue{
		Meta: fileformat.Metadata{FormatVersion: 1, SchemaID: "s", Kind: fileformat.KindAcquisition, Schema: s},
		Items: []fileformat.Item{
			{ID: "new", Facts: map[string]schema.Value{"theme": {Type: schema.Category, Category: "forest"}}},
			{ID: "a", Facts: map[string]schema.Value{"theme": {Type: schema.Category, Category: "sea"}}},
			{ID: "x", Facts: map[string]schema.Value{"theme": {Type: schema.Category, Category: "moon"}}},
		},
	}
	p := filepath.Join(dir, "acq.zip")
	require.NoError(t, fileformat.WriteFile(p, acq))
	return p
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	sp, tp := writeFiles(t, dir, &fileformat.TasteMeta{Blocked: []string{"x"}})
	ap := writeAcquisition(t, dir)
	te := &testEnv{}
	code := run(context.Background(), []string{"check", "--shelf", sp, "--taste", tp, "--acquisition", ap}, te.env())
	require.Equal(t, 0, code, te.stderr.String())
	out := te.stdout.String()
	assert.Contains(t, out, "new  score 0.000\n  passed: not on the blocked list\n  already have 1 like this: i (1.00)\n")
	assert.Contains(t, out, "a  score 0.000\n  passed: not on the blocked list\n  already in the catalogue\n  already have 3 like this: b (1.00), c (1.00), d (1.00)\n")
	assert.Contains(t, out, "x  excluded: on the blocked list\n  already have 0 like this\n")
	assert.NotContains(t, te.stderr.String(), "already in the catalogue", "reported per item, not as a load note")
}

func TestCheckThreshold(t *testing.T) {
	dir := t.TempDir()
	sp, tp := writeFiles(t, dir, nil)
	ap := writeAcquisition(t, dir)
	te := &testEnv{}
	assert.Equal(t, 1, run(context.Background(), []string{"check", "--shelf", sp, "--taste", tp, "--acquisition", ap, "--threshold", "1.5"}, te.env()))
	assert.Contains(t, te.stderr.String(), "--threshold 1.5 is outside [0, 1]")
}
