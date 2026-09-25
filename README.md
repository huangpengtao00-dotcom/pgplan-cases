# pgplan-cases

Three PostgreSQL planner behaviours that turn fast queries into slow ones,
reproduced as tests. Each case builds its own schema, runs `EXPLAIN (ANALYZE,
BUFFERS, FORMAT JSON)`, and the tests assert on the plan — which index, how many
rows thrown away, how many pages read — rather than on timings.

```
eval "$(scripts/local-pg.sh)"  # or point PGPLAN_DSN at any PostgreSQL 15+
go test ./...                 # assertions
go run ./cmd/pgplan           # the numbers below
```

CI runs the cases against PostgreSQL 15, 16 and 17.

## Output

One run on a laptop. Row counts and buffers are deterministic; milliseconds are
not, and are here only for scale.

```
PostgreSQL 16.14 (Homebrew) · scale 1000

partial-index-generic-plan
  rows                         500000 (0.1% pending)
  literal                      Index Scan(jobs_pending_idx) · 0.10 ms · 52 buffers
  $1, custom plan              Index Scan(jobs_pending_idx) · 0.05 ms · 52 buffers
  $1, generic plan             Seq Scan(jobs) · 242.34 ms · 8621 buffers
  → literal and custom plans use jobs_pending_idx=true/true; the generic plan uses it=false

candidates-not-backlog
  backlog 0.01%                Seq Scan(tasks) · 99 matched · 999901 removed by filter · 882.7 ms
  backlog 0.1% (10x)           Seq Scan(tasks) · 999 matched · 999001 removed by filter · 945.8 ms
  backlog 0.1%, partial index  Index Scan(tasks_active_idx) · 999 matched · 0 removed by filter · 3.9 ms
  index size                   full 21.4 MB · partial 40 kB
  → 10x the matching rows cost 1.1x the time; the partial index cut it 244x and the index 549x

keyset-needs-a-tiebreaker
  rows                         20000, ten per timestamp, pages of 25
  created_at > last            16670 rows seen, 3330 lost (17%)
  (created_at, id) > last      20000 rows seen, 0 lost
  plan                         Index Only Scan(events_created_id_idx) · Index Cond (ROW(created_at, id) > ROW('2026-01-01 00:33:19+00'::timestamp with time zone, '20000'::bigint))
  → the single-column cursor lost 3330 of 20000 rows; the row cursor lost none
```

## 1. A partial index the generic plan cannot use

`jobs_pending_idx` covers `created_at WHERE status = 'pending'`. The query
`WHERE status = $1 ORDER BY created_at LIMIT 50` uses it when the planner knows
`$1`: with a literal, or with a custom plan for a prepared statement. With a
generic plan it cannot. To use a partial index the planner has to prove the
query's conditions imply the index predicate, and `status = $1` implies nothing
until `$1` has a value. The generic plan falls back to a sequential scan and
reads 8,621 pages instead of 52.

Prepared statements get generic plans when `plan_cache_mode` forces it, or in
the default `auto` mode after five executions if the generic plan's estimated
cost is not much worse than the custom plans'. Server-side prepared statements
kept by a driver or pooler are how application queries end up there. The fix
used here is to render the predicate as a literal in the SQL text, so every plan
can see it; `plan_cache_mode = force_custom_plan` on the session also works.

## 2. Cost follows candidates, not matches

A sweeper asks for "active" rows below a cutoff. Nothing indexes `status`, so
every row in the range is a candidate: the executor reads about a million rows
and throws away all but the matches. Making the backlog ten times larger
changes the matches from 99 to 999 and leaves the work the same, because the
candidates did not change.

A partial index `ON tasks (updated_at) WHERE status = 'active'` makes the
candidates equal to the matches: zero rows removed by filter, and an index of
40 kB instead of 21.4 MB. When a query is slow, the first number to look at is
"Rows Removed by Filter", not the size of the result.

## 3. Keyset pagination needs a tiebreaker

Paging with `WHERE created_at > $last ORDER BY created_at LIMIT 25` skips every
row that shares the last timestamp of a page. With ten rows per timestamp, one
row in six is never returned. Paging on the row `(created_at, id) > ($t, $id)`
returns every row, and with an index on `(created_at, id)` the comparison is a
single index condition, `ROW(created_at, id) > ROW(...)`.

## Layout

```
plan/       EXPLAIN runner and plan-tree helpers (Uses, Scans, RowsRemoved, Buffers)
cases/      one file per case; *_test.go asserts on the observed plans
cmd/pgplan  prints every case
```

A missing `PGPLAN_DSN` fails the tests instead of skipping them.

## License

MIT
