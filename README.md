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

Then pick from the shelf. The CLI asks the question that narrows the shelf most, and stops at three items or when no question helps:

```
taste-machine pick --shelf DIR/shelf.zip --taste DIR/taste.zip
taste-machine pick --shelf DIR/shelf.zip --taste DIR/taste.zip --one-shot   # use the answers stored in the taste file
```

Every result lists what made it score, the answers it matched and the filters it passed. When filters or answers leave nothing, the CLI says why and exits with code 3.

Check a list of items you are thinking of buying (an acquisition list, in the same file format) against your taste:

```
taste-machine check --shelf DIR/shelf.zip --taste DIR/taste.zip --acquisition LIST.zip
```

Each item gets its score and explanation, plus how many shelf items are already like it (similarity of at least `--threshold`, default 0.6) and the three closest.

## Development

Requires only the Go version in `go.mod`; the formatters and the linter are built from `tools/go.mod`.

```
make check   # vet, build, race tests, formatting, lint, tidiness: the same as CI
```

See `AGENTS.md` for working on the code.
