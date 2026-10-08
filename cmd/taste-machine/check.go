package main

import (
	"fmt"
	"strings"

	"github.com/yaad-index/taste-machine/check"
)

type checkCmd struct {
	Shelf       string  `required:"" type:"existingfile" help:"The shelf to compare against."`
	Taste       string  `required:"" type:"existingfile" help:"The taste file."`
	Acquisition string  `required:"" type:"existingfile" help:"The acquisition list to check."`
	Threshold   float64 `default:"${threshold}" help:"Similarity at which a shelf item counts as like the checked one."`
}

func (c *checkCmd) Run(e env) error {
	if c.Threshold < 0 || c.Threshold > 1 {
		return fmt.Errorf("--threshold %v is outside [0, 1]", c.Threshold)
	}
	mo, err := loadModel(c.Shelf, c.Taste, c.Acquisition, e)
	if err != nil {
		return err
	}
	for _, r := range check.Run(mo, c.Threshold) {
		if r.Excluded != nil {
			_, _ = fmt.Fprintf(e.stdout, "%s  excluded: %s\n", r.ID, r.Excluded)
		} else {
			_, _ = fmt.Fprint(e.stdout, r.Result.Explain())
		}
		if r.AlreadyInCatalogue {
			_, _ = fmt.Fprintln(e.stdout, "  already in the catalogue")
		}
		_, _ = fmt.Fprintf(e.stdout, "  already have %d like this%s\n", r.LikeThis, closest(r.Closest))
	}
	return nil
}

func closest(ns []check.Neighbour) string {
	if len(ns) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, fmt.Sprintf("%s (%.2f)", n.ID, n.Similarity))
	}
	return ": " + strings.Join(parts, ", ")
}
