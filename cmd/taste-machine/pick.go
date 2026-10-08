package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/yaad-index/taste-machine/fileformat"
	"github.com/yaad-index/taste-machine/pick"
	"github.com/yaad-index/taste-machine/score"
)

// exitEmpty is the exit code when filters or answers leave no items.
const exitEmpty = 3

// exitError ends the command with a specific exit code after its message
// has been printed.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

type pickCmd struct {
	tasteFlags `embed:""`
	OneShot    bool     `name:"one-shot" help:"Ask nothing: use the stored answers (one person) or the --answer flags (group mode)."`
	Answer     []string `sep:"none" help:"Group mode, with --one-shot: an answer as field=value (field= for no preference)."`
	Top        int      `default:"10" help:"How many results to show."`
}

func (c *pickCmd) Run(e env) error {
	if len(c.Answer) > 0 && (!c.group() || !c.OneShot) {
		return errors.New("--answer needs --one-shot and group mode; one person's one-shot answers are stored in the taste file")
	}
	taste, single, err := c.load("", e)
	if err != nil {
		return err
	}
	s := pick.New(taste)
	if r := s.Empty(); r != nil {
		printEmpty(e.stdout, *r)
		return exitError{exitEmpty}
	}
	switch {
	case c.OneShot && single != nil:
		err = oneShot(s, e, single.Member.Meta.Answers, func(a fileformat.Answer) (pick.Answer, error) { return pick.StoredAnswer(single, a) })
	case c.OneShot:
		var given []fileformat.Answer
		for _, a := range c.Answer {
			given = append(given, fileformat.Answer{Field: a})
		}
		err = oneShot(s, e, given, func(a fileformat.Answer) (pick.Answer, error) { return pick.ParseAnswer(taste, a.Field) })
	default:
		err = interactive(s, e)
	}
	if err != nil {
		return err
	}
	results := s.Results()
	for i, r := range results {
		if i == c.Top {
			break
		}
		_, _ = fmt.Fprint(e.stdout, r.Explain())
	}
	return nil
}

// oneShot applies answers in order, each read by parse.
func oneShot(s *pick.Session, e env, answers []fileformat.Answer, parse func(fileformat.Answer) (pick.Answer, error)) error {
	for _, stored := range answers {
		a, err := parse(stored)
		if err != nil {
			return err
		}
		out, err := s.Apply(a)
		if err != nil {
			return err
		}
		if out.Unmet {
			_, _ = fmt.Fprintf(e.stderr, "note: no item matches the answer on %s, it gives way\n", a.Field)
		}
		if out.Empty != nil {
			printEmpty(e.stdout, *out.Empty)
			return exitError{exitEmpty}
		}
	}
	return nil
}

func interactive(s *pick.Session, e env) error {
	in := bufio.NewScanner(e.stdin)
	read := func() (string, bool) {
		_, _ = fmt.Fprint(e.stdout, "> ")
		if !in.Scan() {
			return "", false
		}
		return strings.TrimSpace(in.Text()), true
	}
	for {
		q, ok := s.Next()
		if !ok {
			return nil
		}
		_, _ = fmt.Fprintf(e.stdout, "%s? (%d left)\n", q.Field.Name, s.Remaining())
		for i, o := range q.Options {
			_, _ = fmt.Fprintf(e.stdout, "  %d) %s (%d)\n", i+1, o.Label, o.Count)
		}
		_, _ = fmt.Fprintln(e.stdout, "  o) other\n  n) no preference\n  s) stop and show the results")
		a, stop, err := ask(q, read)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
		out, err := s.Apply(a)
		if err != nil {
			return err
		}
		if out.Unmet {
			_, _ = fmt.Fprintln(e.stdout, "No item left has that; the answer gives way.")
		}
		if out.Empty != nil {
			printEmpty(e.stdout, *out.Empty)
			_, _ = fmt.Fprintln(e.stdout, "Undo that answer? [Y/n]")
			line, ok := read()
			if ok && strings.EqualFold(line, "n") {
				return exitError{exitEmpty}
			}
			s.Undo()
		}
	}
}

// ask reads until it gets a valid choice. stop is true for "s" or the end
// of input.
func ask(q pick.Question, read func() (string, bool)) (a pick.Answer, stop bool, err error) {
	for {
		line, ok := read()
		if !ok {
			return pick.Answer{}, true, nil
		}
		switch strings.ToLower(line) {
		case "s":
			return pick.Answer{}, true, nil
		case "n":
			return pick.Answer{Field: q.Field.Name, Kind: pick.NoPreference}, false, nil
		case "o":
			shown := make([]string, 0, len(q.Options))
			for _, o := range q.Options {
				shown = append(shown, o.Key)
			}
			return pick.Answer{Field: q.Field.Name, Kind: pick.Other, Shown: shown}, false, nil
		}
		n, convErr := strconv.Atoi(line)
		if convErr == nil && n >= 1 && n <= len(q.Options) {
			return pick.Answer{Field: q.Field.Name, Key: q.Options[n-1].Key}, false, nil
		}
	}
}

func printEmpty(w io.Writer, r score.EmptyReport) {
	_, _ = fmt.Fprintln(w, "No items left:")
	for _, c := range r.Causes {
		_, _ = fmt.Fprintf(w, "  %d removed: %s\n", c.Items, c.Cause)
	}
}

// exitCode maps a command error to the process exit code.
func exitCode(err error) int {
	var ee exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}
