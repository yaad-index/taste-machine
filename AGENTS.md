# Working on taste-machine

This file is for people and agents changing taste-machine. `README.md` is for people using it.

## What this is

A Go library with a CLI (`cmd/taste-machine`). Design decisions are numbered Architecture Decision Records in `adr/`; read the ADR governing an area before changing that area.

## Packages

- `schema`: field declarations (type, role, weights, buckets) and typed field values.
- `fileformat`: reading and writing the zip files (catalogue, taste, acquisition list).
- `dataset`: loading a shelf, taste files and an acquisition list together, checking they match, and joining them by item id.
- `score`: the v1 matcher of ADR 0002: per-member affinities, filters, the taste score, ranking and explanations.
- `importer`: the interface every source implements; each source lives in its own package below it (`importer/bgg`).
- `cmd/taste-machine`: the CLI, a thin Kong layer over the packages.

## Before pushing

```
make check          # vet, build, race tests, formatting, lint, tidiness: what CI runs
make fmt            # apply gofumpt and goimports, tidy every module
make dist           # cross-compile the CLI for every release platform into dist/
make install-hooks  # optional: run the fast subset of make check on every commit
```

Nothing needs installing besides Go. The formatters and the linter are tool dependencies in `tools/go.mod`, a separate module, and run as `go tool -modfile=tools/go.mod <tool>`. Keeping them in their own module keeps them out of `go.mod`, which every program using the library resolves. Add a new dev tool the same way:

```
go get -modfile=tools/go.mod -tool <package>@<version>
```

## Conventions

- Formatting is gofumpt, with imports grouped by goimports under the local prefix `github.com/yaad-index/taste-machine`.
- Tests use testify (`require` / `assert`) and always run with `-race`.
- Pull request titles are Conventional Commits: squash merges use the title as the commit subject, and releases are computed from it.
- A dependency that is load-bearing for the public API is decided in an ADR before it is added.
