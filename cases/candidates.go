package cases

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/huangpengtao00-dotcom/pgplan-cases/plan"
)

// CandidatesNotBacklog: a sweeper looks for the few "active" rows below a
// cutoff. No index narrows status, so the executor reads every candidate row
// and discards all but the matches. The cost follows the candidates, not the
// matches: grow the matching set tenfold and the time does not move; shrink
// the candidate set with a partial index and it collapses.
func CandidatesNotBacklog(ctx context.Context, conn *pgx.Conn, scale int) (Result, error) {
	r := newResult("candidates-not-backlog")
	if err := schema(ctx, conn, "case_candidates"); err != nil {
		return r, err
	}
	rows := 1000 * scale
	build := func(activeEvery int) error {
		return exec(ctx, conn,
			`DROP TABLE IF EXISTS tasks`,
			`CREATE TABLE tasks (id bigserial PRIMARY KEY, status text NOT NULL, updated_at timestamptz NOT NULL, body text)`,
			fmt.Sprintf(`INSERT INTO tasks (status, updated_at, body)
			   SELECT CASE WHEN g %% %d = 0 THEN 'active' ELSE 'done' END,
			          timestamptz '2026-01-01' + g * interval '1 second', repeat('y', 60)
			   FROM generate_series(1, %d) g`, activeEvery, rows),
			`CREATE INDEX tasks_updated_idx ON tasks (updated_at)`,
			`ANALYZE tasks`,
		)
	}
	sweep := `SELECT id FROM tasks WHERE status = 'active' AND updated_at < timestamptz '2026-01-01' + %d * interval '1 second' ORDER BY updated_at DESC LIMIT 100000`
	cutoff := rows

	measure := func(label string) (plan.Explained, error) {
		if _, err := conn.Exec(ctx, fmt.Sprintf(sweep, cutoff)); err != nil { // warm the cache
			return plan.Explained{}, err
		}
		e, err := plan.Explain(ctx, conn, "ANALYZE, BUFFERS, TIMING OFF", fmt.Sprintf(sweep, cutoff))
		if err != nil {
			return e, err
		}
		r.add(label, "%s · %.0f matched · %.0f removed by filter · %.1f ms",
			strings.Join(e.Plan.Scans(), " "), e.Plan.ActualRows, e.Plan.RowsRemoved(), e.ExecutionTime)
		return e, nil
	}

	if err := build(10000); err != nil {
		return r, err
	}
	small, err := measure("backlog 0.01%")
	if err != nil {
		return r, err
	}
	if err := build(1000); err != nil {
		return r, err
	}
	big, err := measure("backlog 0.1% (10x)")
	if err != nil {
		return r, err
	}
	var fullSize int64
	if err := conn.QueryRow(ctx, `SELECT pg_relation_size('tasks_updated_idx')`).Scan(&fullSize); err != nil {
		return r, err
	}
	if err := exec(ctx, conn,
		`CREATE INDEX tasks_active_idx ON tasks (updated_at) WHERE status = 'active'`,
		`ANALYZE tasks`,
	); err != nil {
		return r, err
	}
	partial, err := measure("backlog 0.1%, partial index")
	if err != nil {
		return r, err
	}
	var partialSize int64
	if err := conn.QueryRow(ctx, `SELECT pg_relation_size('tasks_active_idx')`).Scan(&partialSize); err != nil {
		return r, err
	}
	r.add("index size", "full %s · partial %s", mb(fullSize), mb(partialSize))

	r.Num["small_matched"], r.Num["big_matched"] = small.Plan.ActualRows, big.Plan.ActualRows
	r.Num["small_removed"], r.Num["big_removed"] = small.Plan.RowsRemoved(), big.Plan.RowsRemoved()
	r.Num["small_ms"], r.Num["big_ms"], r.Num["partial_ms"] = small.ExecutionTime, big.ExecutionTime, partial.ExecutionTime
	r.Num["partial_removed"] = partial.Plan.RowsRemoved()
	r.Num["full_size"], r.Num["partial_size"] = float64(fullSize), float64(partialSize)
	r.Bool["partial_used"] = partial.Plan.Uses("tasks_active_idx")
	r.Summary = fmt.Sprintf("10x the matching rows cost %.1fx the time; the partial index cut it %.0fx and the index %.0fx",
		big.ExecutionTime/small.ExecutionTime, big.ExecutionTime/partial.ExecutionTime, float64(fullSize)/float64(partialSize))
	return r, nil
}

func mb(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%d kB", b>>10)
	}
}
