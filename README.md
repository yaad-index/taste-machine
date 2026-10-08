# taste-machine

A domain-agnostic recommendation engine: a Go library and a CLI that score a catalogue of items against one person's taste, both read from files, and explain every result.

Work in progress; the design is recorded in `adr/`.

## Development

Requires only the Go version in `go.mod`; the formatters and the linter are built from `tools/go.mod`.

```
make check   # vet, build, race tests, formatting, lint, tidiness: the same as CI
```

See `AGENTS.md` for working on the code.
