package cases

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/huangpengtao00-dotcom/pgplan-cases/plan"
)

// KeysetTiebreaker: paging by "created_at > last" loses every row that shares
// the last timestamp of a page. Paging by the row (created_at, id) loses none,
// and a composite index still serves it as a single index condition.
func KeysetTiebreaker(ctx context.Context, conn *pgx.Conn, scale int) (Result, error) {
	r := newResult("keyset-needs-a-tiebreaker")
	if err := schema(ctx, conn, "case_keyset"); err != nil {
		return r, err
	}
	rows := 20 * scale
	if err := exec(ctx, conn,
		`CREATE TABLE events (id bigserial PRIMARY KEY, created_at timestamptz NOT NULL)`,
		// ten events share every timestamp, as batch inserts do
		fmt.Sprintf(`INSERT INTO events (created_at) SELECT timestamptz '2026-01-01' + (g / 10) * interval '1 second' FROM generate_series(0, %d) g`, rows-1),
		`CREATE INDEX events_created_id_idx ON events (created_at, id)`,
		`ANALYZE events`,
	); err != nil {
		return r, err
	}
	const page = 25

	naive := 0
	last := time.Time{}
	for {
		rs, err := conn.Query(ctx, `SELECT created_at FROM events WHERE created_at > $1 ORDER BY created_at LIMIT $2`, last, page)
		if err != nil {
			return r, err
		}
		n := 0
		for rs.Next() {
			if err := rs.Scan(&last); err != nil {
				return r, err
			}
			n++
		}
		rs.Close()
		naive += n
		if n < page {
			break
		}
	}

	fixed := 0
	lastT, lastID := time.Time{}, int64(0)
	for {
		rs, err := conn.Query(ctx, `SELECT created_at, id FROM events WHERE (created_at, id) > ($1, $2) ORDER BY created_at, id LIMIT $3`, lastT, lastID, page)
		if err != nil {
			return r, err
		}
		n := 0
		for rs.Next() {
			if err := rs.Scan(&lastT, &lastID); err != nil {
				return r, err
			}
			n++
		}
		rs.Close()
		fixed += n
		if n < page {
			break
		}
	}

	e, err := plan.Explain(ctx, conn, "", `SELECT created_at, id FROM events WHERE (created_at, id) > ($1, $2) ORDER BY created_at, id LIMIT 25`, lastT, lastID)
	if err != nil {
		return r, err
	}
	cond := ""
	e.Plan.Walk(func(n plan.Node) {
		if n.IndexName == "events_created_id_idx" {
			cond = n.IndexCond
		}
	})

	r.add("rows", "%d, ten per timestamp, pages of %d", rows, page)
	r.add("created_at > last", "%d rows seen, %d lost (%.0f%%)", naive, rows-naive, 100*float64(rows-naive)/float64(rows))
	r.add("(created_at, id) > last", "%d rows seen, %d lost", fixed, rows-fixed)
	r.add("plan", "%s · Index Cond %s", strings.Join(e.Plan.Scans(), " "), cond)
	r.Num["rows"], r.Num["naive_seen"], r.Num["fixed_seen"] = float64(rows), float64(naive), float64(fixed)
	r.Bool["row_compare_in_index_cond"] = strings.Contains(cond, "ROW(")
	r.Summary = fmt.Sprintf("the single-column cursor lost %d of %d rows; the row cursor lost none", rows-naive, rows)
	return r, nil
}
