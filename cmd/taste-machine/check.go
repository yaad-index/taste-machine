package main

import (
	"fmt"
	"strings"

	"github.com/yaad-index/taste-machine/check"
	"github.com/yaad-index/taste-machine/score"
)

type checkCmd struct {
	tasteFlags  `embed:""`
	Acquisition string  `required:"" type:"existingfile" help:"The acquisition list to check."`
	Threshold   float64 `default:"${threshold}" help:"Similarity at which a shelf item counts as like the checked one."`
}

func (c *checkCmd) Run(e env) error {
	if c.Threshold < 0 || c.Threshold > 1 {
		return fmt.Errorf("--threshold %v is outside [0, 1]", c.Threshold)
	}
	taste, _, err := c.load(c.Acquisition, e)
	if err != nil {
		return err
	}
	for _, r := range check.Run(taste, c.Threshold) {
		if r.Excluded != nil {
			verb := "excluded"
			if r.Excluded.Member != "" && r.Excluded.Kind == score.ByList {
				verb = "vetoed"
			}
			_, _ = fmt.Fprintf(e.stdout, "%s  %s: %s\n", score.Label(r.ID, r.Name), verb, r.Excluded)
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
		parts = append(parts, fmt.Sprintf("%s (%.2f)", score.Label(n.ID, n.Name), n.Similarity))
	}
	return ": " + strings.Join(parts, ", ")
}
