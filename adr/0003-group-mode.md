# ADR 0003: Group mode (v1)

## Context
A shelf is often used by several people at once: they want one pick that suits the whole group. ADR 0002 scores one taste file against a shelf. This ADR extends it to several taste files over the same shelf, with no change to the single-user rules.

## Decision

### 1. Inputs
- One shelf and N ≥ 2 taste files (fewer is an error; one person uses single-user mode), one per member. Each taste file is validated against the shelf as in ADR 0001 (same `schema_id`), and each may carry its own learn-from catalogue.
- Each member has a label, taken from the taste metadata (`label`), or the file name when absent. Labels must be unique.
- Members are processed in label order, so the output does not depend on the order the files are given.

### 2. Per-member scoring
- Each member's affinities, taste score and item lists are computed from their own taste file, with their own constants (k, p, P), exactly as in ADR 0002. Members' B values are ignored (see section 4).

### 3. Filters and item lists
- **Group size:** the schema may mark at most one `range` field as the group-size field (`group_size: true`). That field is checked against N, or against the number the user gives instead. This value replaces the members' own values for that field. With no field marked, there is no implicit filter. `group_size` on a non-`range` field, or on more than one field, is a load error.
- **Every member's other hard filters apply.** An item excluded for any member is excluded for the group.
- **A `blocked` item of any member is excluded** (a veto). The report names which list excluded it, by member label.
- **A `favourites` item** sets that member's taste score to 1 for that item, as in ADR 0002. It does not override another member's `blocked` list.

### 4. Group taste score
- **Group taste score = the mean of the members' taste scores**, so it lies in [−1, 1].
- **Ties break by the lowest member score, highest first** (the option that leaves nobody worst off), then by the mean rating over the members who rated it (unrated last), then by `id` ascending.
- **Final score** = group taste score + B × the mean match over answered preferences, with B = 1 (B comes from the command line in group mode, default 1).

### 5. Questions
- Questions go to the group, and the group gives **one** answer per question. Answers narrow and score exactly as in ADR 0002.
- Question selection is the ADR 0002 rule, unchanged: it depends on remaining, not on taste.
- The 3 options shown are ordered by the **mean** of the members' affinities, then by count in remaining.
- One-shot mode takes answers from the command line, not from any taste file.

### 6. Explanation
Every result lists:
- its group score and each member's score, by label;
- the top contributions per member (top 2 positive, top 1 negative);
- the answers it matched and the filters it passed;
- which members have it on their `favourites` list.
Excluded items are counted per cause (a filter by member label, or `blocked` by member label), so the group can see why a candidate is missing.

### 7. `check` in group mode
- Each acquisition-list item is scored for every member and for the group, with the same aggregation.
- An item on any member's `blocked` list is reported as vetoed, not scored.

## Consequences
- The mean favours items most of the group likes, and the tie-break favours fairness. A strongly disliked item can still win on a high mean; that is accepted for v1, and an aggregation alternative (for example, least misery) needs its own ADR.
- Vetoes are absolute. A large group with long `blocked` lists can empty the shelf; the empty-result report (ADR 0002 section 5, the item-list cause) names the vetoes by member label.
- Members' scores are visible to the whole group in the explanation. That is intended: the point is a shared decision.
- There is no per-member weighting and no separate answers per member in v1.
