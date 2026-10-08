package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fzerorubigd/bggo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/importer/bgg"
)

type fakeSource struct{}

func (fakeSource) GetCollection(context.Context, bggo.GetCollectionRequest) ([]bggo.CollectionItem, error) {
	return []bggo.CollectionItem{{ID: 7, Status: []bggo.CollectionStatus{bggo.CollectionOwn}, NumPlays: 2}}, nil
}

func (fakeSource) GetThings(_ context.Context, req bggo.GetThingsRequest) ([]bggo.ThingResult, error) {
	return []bggo.ThingResult{{ID: req.IDs[0], Name: "seven", MinPlayers: 1, MaxPlayers: 2}}, nil
}

type testEnv struct {
	stdin          string
	stdout, stderr bytes.Buffer
	vars           map[string]string
	gotKey         string
	gotInterval    time.Duration
}

func (te *testEnv) env() env {
	return env{
		stdin:  strings.NewReader(te.stdin),
		stdout: &te.stdout,
		stderr: &te.stderr,
		getenv: func(k string) string { return te.vars[k] },
		newSource: func(key string, interval time.Duration) bgg.Source {
			te.gotKey, te.gotInterval = key, interval
			return fakeSource{}
		},
	}
}

func TestRunVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		te := &testEnv{}
		assert.Equal(t, 0, run(context.Background(), []string{arg}, te.env()), arg)
		assert.Equal(t, "taste-machine dev\n", te.stdout.String(), arg)
		assert.Empty(t, te.stderr.String(), arg)
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"pick"}, {"version", "extra"}, {"compile", "bgg"}} {
		te := &testEnv{}
		assert.Equal(t, 2, run(context.Background(), args, te.env()), args)
		assert.Empty(t, te.stdout.String(), args)
		assert.Contains(t, te.stderr.String(), "Usage: taste-machine", args)
	}
}

func TestRunHelp(t *testing.T) {
	te := &testEnv{}
	assert.Equal(t, 0, run(context.Background(), []string{"compile", "bgg", "--help"}, te.env()))
	assert.Contains(t, te.stdout.String(), "--user=STRING")
	assert.Contains(t, te.stdout.String(), "TASTE_MACHINE_BGG_API_KEY")
}

func TestCompileBGG(t *testing.T) {
	out := filepath.Join(t.TempDir(), "new")
	te := &testEnv{vars: map[string]string{apiKeyEnv: "k"}}
	code := run(context.Background(), []string{"compile", "bgg", "--user", "someone", "--out", out, "--interval", "5s"}, te.env())
	require.Equal(t, 0, code, te.stderr.String())
	assert.Equal(t, "k", te.gotKey)
	assert.Equal(t, 5*time.Second, te.gotInterval)
	assert.Contains(t, te.stdout.String(), filepath.Join(out, "shelf.zip"))

	shelf, err := fileformat.ReadCatalogueFile(filepath.Join(out, "shelf.zip"))
	require.NoError(t, err)
	assert.Len(t, shelf.Items, 1)
	taste, err := fileformat.ReadTasteFile(filepath.Join(out, "taste.zip"))
	require.NoError(t, err)
	assert.Equal(t, "someone", taste.Meta.Taste.Label)
}

func TestCompileBGGNeedsKey(t *testing.T) {
	out := t.TempDir()
	te := &testEnv{}
	assert.Equal(t, 1, run(context.Background(), []string{"compile", "bgg", "--user", "someone", "--out", out}, te.env()))
	assert.Contains(t, te.stderr.String(), "set TASTE_MACHINE_BGG_API_KEY")
	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing is written without a key")
}
