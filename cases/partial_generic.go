package cases

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/huangpengtao00-dotcom/pgplan-cases/plan"
)

// PartialIndexGenericPlan: a partial index on "status = 'pending'" serves a
// query with the literal. The same query with status bound as a parameter
// uses it under a custom plan, and cannot under a generic plan: the planner
// has to prove the index predicate from the query, and "status = $1" proves
// nothing until $1 has a value. Drivers and poolers that keep server-side
// prepared statements are how a query ends up on the generic plan.
func PartialIndexGenericPlan(ctx context.Context, conn *pgx.Conn, scale int) (Result, error) {
	r := newResult("partial-index-generic-plan")
	if err := schema(ctx, conn, "case_generic"); err != nil {
		return r, err
	}
	rows := 500 * scale
	if err := exec(ctx, conn,
		`CREATE TABLE jobs (id bigserial PRIMARY KEY, status text NOT NULL, created_at timestamptz NOT NULL, payload text)`,
		fmt.Sprintf(`INSERT INTO jobs (status, created_at, payload)
		   SELECT CASE WHEN g %% 1000 = 0 THEN 'pending' ELSE 'done' END,
		          timestamptz '2026-01-01' + g * interval '1 second', repeat('x', 80)
		   FROM generate_series(1, %d) g`, rows),
		`CREATE INDEX jobs_pending_idx ON jobs (created_at) WHERE status = 'pending'`,
		`ANALYZE jobs`,
		`PREPARE next_jobs(text) AS SELECT id FROM jobs WHERE status = $1 ORDER BY created_at LIMIT 50`,
	); err != nil {
		return r, err
	}
	defer conn.Exec(ctx, "DEALLOCATE next_jobs")
	defer conn.Exec(ctx, "RESET plan_cache_mode")

	if err := exec(ctx, conn, `SELECT count(*) FROM jobs`); err != nil { // warm the cache
		return r, err
	}
	literal, err := plan.Explain(ctx, conn, "ANALYZE, BUFFERS, TIMING OFF", `SELECT id FROM jobs WHERE status = 'pending' ORDER BY created_at LIMIT 50`)
	if err != nil {
		return r, err
	}
	modes := map[string]plan.Explained{}
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		if err := exec(ctx, conn, "SET plan_cache_mode = "+mode); err != nil {
			return r, err
		}
		e, err := plan.Explain(ctx, conn, "ANALYZE, BUFFERS, TIMING OFF", `EXECUTE next_jobs('pending')`)
		if err != nil {
			return r, err
		}
		modes[mode] = e
	}
	custom, generic := modes["force_custom_plan"], modes["force_generic_plan"]

	r.add("rows", "%d (0.1%% pending)", rows)
	r.add("literal", "%s · %.2f ms · %d buffers", strings.Join(literal.Plan.Scans(), " "), literal.ExecutionTime, literal.Plan.Buffers())
	r.add("$1, custom plan", "%s · %.2f ms · %d buffers", strings.Join(custom.Plan.Scans(), " "), custom.ExecutionTime, custom.Plan.Buffers())
	r.add("$1, generic plan", "%s · %.2f ms · %d buffers", strings.Join(generic.Plan.Scans(), " "), generic.ExecutionTime, generic.Plan.Buffers())
	r.Summary = fmt.Sprintf("literal and custom plans use jobs_pending_idx=%v/%v; the generic plan uses it=%v",
		literal.Plan.Uses("jobs_pending_idx"), custom.Plan.Uses("jobs_pending_idx"), generic.Plan.Uses("jobs_pending_idx"))
	r.Bool["literal_uses_partial"] = literal.Plan.Uses("jobs_pending_idx")
	r.Bool["custom_uses_partial"] = custom.Plan.Uses("jobs_pending_idx")
	r.Bool["generic_uses_partial"] = generic.Plan.Uses("jobs_pending_idx")
	r.Num["custom_buffers"] = float64(custom.Plan.Buffers())
	r.Num["generic_buffers"] = float64(generic.Plan.Buffers())
	return r, nil
}
