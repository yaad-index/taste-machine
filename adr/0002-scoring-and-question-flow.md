# ADR 0002: Schema, scoring and question flow (v1)

## Context
ADR 0001 sets the architecture and leaves the matching logic here. v1 matching is deterministic (no ML, no LLM) and explainable. It must work for any domain from the schema alone.

## Decision

### 1. Field types and roles
Every field (catalogue schema or taste metadata) declares `type`, `role` (`filter` | `preference` | `both` | `info`), `askable`, and an optional `weight` (default 1). The schema is validated at load time; an invalid type and role combination is an error.

| type | meaning | allowed roles | affinity | similarity |
|---|---|---|---|---|
| `category` | one value | any | per value | 1 if equal, 0 if not |
| `bool` | true or false | any | as a 2-value category | as category |
| `set` | many values | any | per value | Jaccard |
| `number` | numeric | any | per bucket | 1 − \|Δbucket\| / buckets |
| `range` | min and max ("fits N") | `filter`, `info` | none | none |
| `votes` | per-value counts (polls) | `preference`, `info` | per value, weighted by each value's share | 1 − total-variation distance |

**Buckets for `number`:** declared edges in the catalogue metadata, or quantile edges computed from the **shelf's** distinct values (5 by default, set in catalogue metadata only). Tied values collapse buckets, so fewer than 5 is possible. Learn-from items are bucketed with the shelf's edges.

### 2. Taste signal per item
- **Rating:** with the user's mean m on scale [lo, hi] (direction already applied):
  - signal = (r − m) / (hi − m) when r ≥ m;
  - signal = (r − m) / (m − lo) when r < m.
  - Both ends map to ±1, and the user's average maps to 0.
- **Plays without a rating:** signal = p × min(1, log2(plays) / log2(P)), with p = 0.5 and P = 10.
  - One play gives 0, which reads as "tried it", not "liked it".
  - The signal rises with replays up to p, and is always weaker than a strong rating.
- **Both a rating and plays:** the rating wins.
- **Zero denominators:** if hi − m or m − lo is 0 (for example, every rating is the same), the signal is 0. Zero plays and no rating also give 0.

### 3. Affinity per value
- affinity(v) = Σ signal / (n + k), with k = 3. The sum runs over the n items that have value v.
- The items counted in n are the member's history: taste entries with a rating or at least one play. An entry with neither says nothing about taste and is not counted; one play counts, with signal 0.
- For `votes`, each item contributes signal × share(v), and n is Σ share(v), not the item count, so votes affinities sit on the same scale as category ones.
- **A stated preference in the taste file** replaces affinity(v): like is +1, dislike is −1.
- **"never"** is allowed only on fields whose role includes `filter`. It acts as that field's filter, so filtering still has one path. On other fields it is a load error.

### 4. Taste score of an item
- **Field score** = the mean of the affinities of the item's values for that field.
  - Values the user has never rated count as 0 inside the mean. This is intentional: a game full of unknown mechanics is uncertain, not liked.
- **Taste score** = the weighted mean of the field scores over fields whose role is `preference` or `both`, so it lies in [−1, 1].
- **A missing field** contributes 0 and is still counted in the mean.
- **Items without a field** are dropped only by a filter on that field whose `missing` policy is `drop`. The default is `keep`.

### 5. Filters
- Filters are applied first, after joining catalogue and taste by `id`.
- A `range` filter passes when N is in [min, max].
- **If filters leave 0 items**, the message names the cause:
  - the declared filter, when filters emptied the set before any question was asked;
  - the answer, when an answer emptied it;
  - the item list (section 5b), when blocked items emptied it.
- Interactive mode offers to remove that answer. One-shot mode prints the report and exits with code 3.
- A filter is never relaxed silently.

### 5b. Item lists
- The taste file may carry two lists of item ids: `blocked` and `favourites`.
- The names differ from the field value "never" in section 3 on purpose: that one filters by attribute value, these by item id.
- **`blocked`** is applied as a hard filter on `id`, through the section 5 path, so it excludes the item whatever it scores and filtering keeps one path. The report names the list as the cause.
- **`favourites`** sets the item's taste score to 1, the maximum. Hard filters still apply (a `favourites` item that does not fit the player count is still excluded), and answers still narrow it like any other item. The explanation still lists the computed contributions and says the score shown is 1 because of the list.
- List ids resolve against the shelf or the acquisition list. An id on both `blocked` and `favourites` is a load error. An id in neither is reported and ignored.
- In `check`, a `blocked` item is reported as excluded and not scored; a `favourites` item gets taste score 1 and is flagged in the result.
- The lists do not change learning: a rated item on either list still contributes its signal to affinities.

### 6. Answers during `pick` (session only, never written back to the taste file)
- **A filter answer** narrows **remaining**.
- **A preference answer** narrows remaining to the items that **match it for narrowing**, if any exist. If none match, it gives way: remaining is unchanged and the answer is reported as unmet.
- An answer on a `both` field is a preference answer: it narrows and scores, and gives way when nothing matches. The field's hard filter is a stored "never" in the taste file.
- **"Matches for narrowing"** means:
  - set, category, bool: the item has the value;
  - number: the same bucket;
  - votes: the value has the item's top share (ties count).
- The graded match below is used only for scoring, never for narrowing.
- **An item missing the field:** the field's `missing` policy also applies to answers. With `keep` (the default) the item survives the narrowing; with `drop` it does not.
- match(item, answer):
  - set or category: 1 if the item has the value, else 0;
  - number: 1 − |Δbucket| / buckets;
  - votes: the share of that value.
- **Final score** = taste score + B × the mean match over answered preferences, with B = 1.
  - Both terms lie in [−1, 1] and [0, 1] respectively, so an answer counts as much as the whole taste history.
  - Taste and answers are separate terms, so a stated preference and an answer on the same value are not double-counted. One is history, the other is "right now".
- **"No preference"** skips the field.
- **"Other"** keeps the items that have none of the offered values, then offers the next 3.
  - On a `range` field the fits-N options overlap, so they are shown in numeric order, and "other" only shows the next numbers and narrows nothing; narrowing happens when a number is picked.

### 7. Question selection
- **Candidate questions:** askable, unanswered fields whose options split remaining into at least 2 non-empty groups.
- **Options per type:**
  - category, bool: values;
  - set: the values present in remaining (an item counts under every value it has);
  - number: buckets;
  - range: "fits N" for the N values present;
  - votes: values.
- **Pick the field with the lowest expected remaining size after an answer.**
  - Groups are taken over **all** options of the field (not only the 3 shown), plus a "none" group for items without the field.
  - With group sizes g and total G = Σ|g| (G can exceed |remaining| when set values overlap), P(g) = |g| / G and the expected remaining size is Σ|g|² / G.
  - Membership uses the "matches for narrowing" rule in section 6.
- **Group size first:** in single-user mode, the field marked `group_size` is asked first when it is askable, before the expected-size rule. In group mode it is not asked (ADR 0003 sets it from N).
- **Ties** break by schema order, then by field name.
- **The 3 options shown** are ordered by the user's affinity (highest first), then by count in remaining. "Other" and "no preference" are always added.
- **Stop** when remaining has 3 or fewer items, or no question splits it.
- **One-shot mode** uses the answers stored in the taste file and asks nothing.

### 8. Ranking and determinism
- **Sort order:** final score descending, then the user's rating descending, with unrated items last, then `id` ascending.
- **Constants** are named in code: k = 3, p = 0.5, P = 10, B = 1, 5 buckets, similarity threshold 0.6.
  - k, p, P and B can be overridden in taste metadata.
  - Buckets can be overridden in catalogue metadata.
- Iteration is always over sorted keys.
- A golden test checks that the same files always give the same output.

### 9. Explanation
Every result lists:
- its top 3 positive and top 2 negative contributions, each with field, value and amount;
- the answers it matched;
- the filters it passed;
- whether it is on the `favourites` list.

### 10. `check` similarity
- **Item similarity** = Σ over the fields whose role is `preference` or `both` (and so not `range`, `info` or filter-only) that are present on either item, of weight × field similarity, divided by Σ weight. A field missing on one item scores 0.
- **"Already have N like this"** counts the shelf items with similarity ≥ 0.6 and lists the 3 closest.
- The 0.6 threshold is a first guess, to be tuned on real data.

## Consequences
- Every number is fixed and visible, so results can be argued with and tuned.
- Buckets come from the shelf. The same weight can bucket differently on another shelf. This is accepted, because affinities are computed per shelf.
- Answers can override taste history. This is intended: the question flow serves the current mood.
- The constants are first guesses, tuned against real data before 1.0.
