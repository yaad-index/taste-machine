// Command taste-machine is the CLI for the taste-machine engine.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/alecthomas/kong"
	"github.com/fzerorubigd/bggo"

	"github.com/yaad-index/taste-machine/importer/bgg"
)

// version is set at release build time with -ldflags "-X main.version=...".
var version = "dev"

// env is what the commands take from the outside world, so tests can
// replace it.
type env struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	// newSource builds the client for the board game importer.
	newSource func(apiKey string, interval time.Duration) bgg.Source
}

type cli struct {
	Version kong.VersionFlag `help:"Print the version and exit."`
	Compile compileCmd       `cmd:"" help:"Compile a source into a shelf and a taste file."`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], env{
		stdout: os.Stdout,
		stderr: os.Stderr,
		getenv: os.Getenv,
		newSource: func(apiKey string, interval time.Duration) bgg.Source {
			return bggo.NewClient(apiKey, bggo.WithLimiter(&bgg.Limiter{Interval: interval}))
		},
	})
	stop()
	os.Exit(code)
}

// exit carries a code out of kong's exit hook, which must not return.
type exit int

func run(ctx context.Context, args []string, e env) (code int) {
	if len(args) == 1 && args[0] == "version" {
		args = []string{"--version"}
	}
	defer func() {
		if r := recover(); r != nil {
			c, ok := r.(exit)
			if !ok {
				panic(r)
			}
			code = int(c)
		}
	}()
	var c cli
	parser, err := kong.New(&c,
		kong.Name("taste-machine"),
		kong.Description("A domain-agnostic recommendation engine."),
		kong.Writers(e.stdout, e.stderr),
		kong.Exit(func(code int) { panic(exit(code)) }),
		kong.Vars{"version": "taste-machine " + version},
		kong.BindTo(ctx, (*context.Context)(nil)),
		kong.Bind(e),
	)
	if err != nil {
		_, _ = fmt.Fprintln(e.stderr, "taste-machine:", err)
		return 1
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		_, _ = fmt.Fprintln(e.stderr, "taste-machine:", err)
		var pe *kong.ParseError
		if errors.As(err, &pe) {
			// Usage after a mistake goes to stderr, like the error.
			parser.Stdout = e.stderr
			_ = pe.Context.PrintUsage(true)
		}
		return 2
	}
	if err := kctx.Run(); err != nil {
		_, _ = fmt.Fprintln(e.stderr, "taste-machine:", err)
		return 1
	}
	return 0
}
