package cases_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/huangpengtao00-dotcom/pgplan-cases/cases"
)

// The cases need a real PostgreSQL. A missing DSN is a failure, not a skip:
// a planner test that silently skips has proven nothing.
func connect(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn := os.Getenv("PGPLAN_DSN")
	if dsn == "" {
		t.Fatal("PGPLAN_DSN is not set; see scripts/local-pg.sh")
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

const scale = 1000

func TestGenericPlanCannotUseThePartialIndex(t *testing.T) {
	r, err := cases.PartialIndexGenericPlan(context.Background(), connect(t), scale)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Bool["literal_uses_partial"] || !r.Bool["custom_uses_partial"] {
		t.Fatalf("literal and custom plans should use the partial index: %+v", r.Facts)
	}
	if r.Bool["generic_uses_partial"] {
		t.Fatalf("a generic plan cannot prove status = $1 implies status = 'pending': %+v", r.Facts)
	}
	if r.Num["generic_buffers"] < 10*r.Num["custom_buffers"] {
		t.Fatalf("generic plan should touch far more pages: %+v", r.Facts)
	}
}

func TestCostFollowsCandidatesNotMatches(t *testing.T) {
	r, err := cases.CandidatesNotBacklog(context.Background(), connect(t), scale)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Facts {
		t.Log(f.Label, "—", f.Value)
	}
	if r.Num["big_matched"] < 9*r.Num["small_matched"] {
		t.Fatalf("setup: the second backlog should be ~10x the first: %+v", r.Num)
	}
	if !r.Bool["partial_used"] || r.Num["partial_removed"] != 0 {
		t.Fatalf("the partial index should hand over only matching rows: %+v", r.Facts)
	}
	if r.Num["partial_size"]*50 > r.Num["full_size"] {
		t.Fatalf("partial index should be a small fraction of the full one: %+v", r.Facts)
	}
}

func TestKeysetWithoutTiebreakerLosesRows(t *testing.T) {
	r, err := cases.KeysetTiebreaker(context.Background(), connect(t), scale)
	if err != nil {
		t.Fatal(err)
	}
	if r.Num["naive_seen"] >= r.Num["rows"] {
		t.Fatalf("the single-column cursor should lose rows at page boundaries: %+v", r.Facts)
	}
	if r.Num["fixed_seen"] != r.Num["rows"] {
		t.Fatalf("the row cursor must see every row: %+v", r.Facts)
	}
	if !r.Bool["row_compare_in_index_cond"] {
		t.Fatalf("the row comparison should be an index condition: %+v", r.Facts)
	}
}
