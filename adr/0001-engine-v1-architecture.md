# ADR 0001: Taste Machine engine, v1 architecture

## Context
People keep collections (games, films, books) and want help choosing: what to use from what they have, and whether a new item suits them. Existing tools are tied to one domain or one service, need a live backend, or hide their reasoning.

## Decision
A domain-agnostic recommendation engine, written as a Go library with a CLI.

1. **Inputs are files, and the files are the database.** The engine never fetches at run time.
   - **Catalogue (the shelf):** a set of items with their facts (attributes, plus source-wide aggregates captured at compile time). Membership and facts change only by recompiling, never by mutation, so it is immutable per compile. One shelf can be scored against many people's tastes.
   - **Taste:** everything about one user that changes: their ratings, play counts and stated preferences. It may carry its own **learn-from catalogue** (the facts of items that person rated), so their whole history counts even when scoring over someone else's shelf.
   - **Acquisition list (optional):** a catalogue of items to evaluate for buying. The shrinking result set during the question flow is called **remaining**, so the two names never clash.
2. **Item identity.** Each item has a stable `id`, unique within its schema. The catalogue owns the item facts; the taste file refers to items by `id`.
   - Affinities are learned from rated items' facts. An id is looked up in the shelf first, then in the taste's learn-from catalogue; **when both have it, the shelf's facts win** (the newer compile). An id found in neither is reported and ignored.
   - The learn-from catalogue must carry the same `schema_id` as the shelf (the rule in decision 3 applies).
   - An acquisition-list item already in the shelf is reported as "already in the catalogue".
3. **File format:** a zip containing `metadata.json` and `items.jsonl`, one item per line.
   - `metadata.json` carries `format_version`, a `schema_id` plus the schema itself (or a reference to it), and what the file is.
   - The engine refuses files whose `schema_id` values do not match. It never guesses.
4. **Ratings:** the scale is declared in the taste metadata.
   - The declaration gives min, max, direction (higher better or lower better) and whether decimals are allowed.
   - The engine normalises the scale. The default is 1 to 10, higher better.
5. **Fields come in two kinds, and each file's metadata marks which is which.** Item attributes are declared in the catalogue schema; per-user fields (rating, plays, owned) are declared in the taste metadata. The engine joins catalogue and taste by `id` first, so every filter or preference is a declared field from either file: one generic path, no special cases.
   - **Filters are hard.** Examples: "can play with 4", "not online", "never played". An item that fails a filter is excluded, however well it scores.
   - **Preferences are soft.** Examples: "heavy", "best at 4", a mechanic. They order and tie-break the items that pass the filters. When nothing matches a preference, the preference gives way.
6. **Plays are taste.** The play count lives in the taste file. It counts as a signal of liking, weaker than an explicit rating, and can also be filtered on.
7. **v1 matching is deterministic, with no ML and no LLM.** This is a v1 choice, not a permanent rule. Scoring sits behind an interface, so a later ADR can add other matchers.
   - The same input gives the same output: fixed parameters, explicit tie-breaks, and no reliance on map iteration order.
   - Every result carries its explanation.
8. **ADR 0002 defines the rest of the logic:**
   - scoring;
   - normalisation across list lengths;
   - numeric attributes;
   - the question-selection rule;
   - missing values and "no preference";
   - empty results;
   - the similarity measure for `check`;
   - the weight of a play relative to a rating.

   v1 does not start before 0002 is accepted.
9. **Compilation is a separate step done by importers, which are plug-ins.** The core knows no data source.
   - v1 ships one BoardGameGeek importer: the collection goes to the catalogue; ratings and plays go to the taste file.
   - BGG requires an API key.
   - Queued (HTTP 202) responses must be retried on every endpoint the importer uses, and rate limits apply. The retry belongs in the `bggo` client library, not in an importer workaround.

## v1 scope
- `compile`: run an importer to produce the files.
- `pick`: an interactive CLI, plus a one-shot mode that uses answers stored in the taste file.
- `check`: score each acquisition-list item against the taste. Report the score, the explanation, and the closest catalogue items.

## Out of scope (later, each with its own ADR)
- An explorer website generated from a catalogue.
- A Telegram frontend.
- Importers for other domains.
- A WebAssembly build.
- ML or LLM matchers.

## Consequences
- A new domain means a new importer and a new schema, never an engine change.
- Recommendations are only as good as the schema's attributes. When attributes are sparse, the engine says little, and it says so.
- With no live backend there is no cross-user signal. That is accepted for v1.
- Sharing catalogues publicly later (an explorer site) needs the source's data terms checked first.
