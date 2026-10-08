package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/importer/bgg"
)

// apiKeyEnv names the variable the board game importer reads its API key
// from. The key is never taken from a flag, so it stays out of shell
// history and process listings.
const apiKeyEnv = "TASTE_MACHINE_BGG_API_KEY"

type compileCmd struct {
	BGG compileBGGCmd `cmd:"" name:"bgg" help:"Compile a board game collection. Reads the API key from TASTE_MACHINE_BGG_API_KEY."`
}

type compileBGGCmd struct {
	User     string        `required:"" help:"The collection's user name."`
	Out      string        `default:"." type:"path" help:"Directory to write shelf.zip and taste.zip into."`
	Label    string        `help:"Label for the taste file (default: the user name)."`
	Interval time.Duration `default:"2s" help:"Minimum time between requests."`
}

func (c *compileBGGCmd) Run(ctx context.Context, e env) error {
	key := e.getenv(apiKeyEnv)
	if key == "" {
		return fmt.Errorf("set %s to the API key", apiKeyEnv)
	}
	im := &bgg.Importer{Source: e.newSource(key, c.Interval), Username: c.User, Label: c.Label}
	shelf, taste, err := im.Compile(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.Out, 0o755); err != nil {
		return err
	}
	shelfPath, tastePath := filepath.Join(c.Out, "shelf.zip"), filepath.Join(c.Out, "taste.zip")
	if err := errors.Join(fileformat.WriteFile(shelfPath, shelf), fileformat.WriteFile(tastePath, taste)); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.stdout, "wrote %s and %s\n", shelfPath, tastePath)
	return err
}
