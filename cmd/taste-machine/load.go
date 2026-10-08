package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yaad-index/taste-machine/dataset"
	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/group"
	"github.com/yaad-index/taste-machine/score"
)

// tasteFlags are the flags pick and check share to load a taste: one
// taste file for one person, or several for group mode.
type tasteFlags struct {
	Shelf        string   `required:"" type:"existingfile" help:"The shelf."`
	Taste        []string `required:"" type:"existingfile" sep:"none" help:"A taste file. Give it more than once for group mode."`
	Size         int      `help:"Group mode: the group size checked against the group-size field (default: the number of taste files)."`
	AnswerWeight *float64 `name:"answer-weight" help:"Group mode: B, the weight of answers against the taste score (default 1)."`
}

func (t *tasteFlags) group() bool { return len(t.Taste) > 1 }

// load reads the files. It returns the taste to score with, and the single
// member's model when there is one taste file. acquisitionPath may be "".
func (t *tasteFlags) load(acquisitionPath string, e env) (score.Taste, *score.Model, error) {
	if !t.group() && (t.Size != 0 || t.AnswerWeight != nil) {
		return nil, nil, errors.New("--size and --answer-weight apply to group mode only (more than one --taste)")
	}
	shelf, err := fileformat.ReadCatalogueFile(t.Shelf)
	if err != nil {
		return nil, nil, err
	}
	var inputs []dataset.Input
	for _, p := range t.Taste {
		tf, err := fileformat.ReadTasteFile(p)
		if err != nil {
			return nil, nil, err
		}
		inputs = append(inputs, dataset.Input{Taste: tf, Name: strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))})
	}
	var acq *fileformat.Catalogue
	if acquisitionPath != "" {
		if acq, err = fileformat.ReadCatalogueFile(acquisitionPath); err != nil {
			return nil, nil, err
		}
	}
	d, err := dataset.Load(shelf, inputs, acq)
	if err != nil {
		return nil, nil, err
	}
	for _, n := range d.Notices {
		if n.Kind == dataset.AlreadyInCatalogue {
			continue // check reports it per item
		}
		who := ""
		if t.group() {
			who = " (" + n.Member + ")"
		}
		_, _ = fmt.Fprintf(e.stderr, "note: %s %s%s, ignored\n", n.Kind, n.ID, who)
	}
	if !t.group() {
		mo := score.Learn(d, d.Members[0])
		return mo, mo, nil
	}
	b := score.DefaultAnswerWeight
	if t.AnswerWeight != nil {
		b = *t.AnswerWeight
	}
	g, err := group.New(d, t.Size, b)
	if err != nil {
		return nil, nil, err
	}
	return g, nil, nil
}
