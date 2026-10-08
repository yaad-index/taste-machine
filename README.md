# taste-machine

A domain-agnostic recommendation engine: a Go library and a CLI that score a catalogue of items against one person's taste, both read from files, and explain every result.

Work in progress; the design is recorded in `adr/`.

## Usage

Compile a board game collection into a shelf (`shelf.zip`, the owned games) and a taste file (`taste.zip`, ratings and plays):

```
export TASTE_MACHINE_BGG_API_KEY=...   # the source requires an API key
taste-machine compile bgg --user NAME --out DIR
```

Requests are spaced at least `--interval` apart (default 2s). The API key is only read from the environment.

## Development

Requires only the Go version in `go.mod`; the formatters and the linter are built from `tools/go.mod`.

```
make check   # vet, build, race tests, formatting, lint, tidiness: the same as CI
```

See `AGENTS.md` for working on the code.
