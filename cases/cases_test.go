package cases_test

import (
	"context"
	"os"
	"regexp"
	"strings"
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

// The README shows one run's output. Timings, page counts and ratios vary by
// machine and PostgreSQL version and are masked; every other token — row
// counts, rows removed, rows lost, which index each plan used — must match a
// run of the current code, so the README cannot keep describing old behaviour.
func TestReadmeOutputMatchesARun(t *testing.T) {
	b, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	start := strings.Index(readme, "## Output")
	start += strings.Index(readme[start:], "```\n") + 4
	end := start + strings.Index(readme[start:], "```")
	block := readme[start:end]
	block = block[strings.Index(block, "\n\n")+2:] // drop the version header

	conn := connect(t)
	var got []string
	for _, c := range cases.All {
		r, err := c.Run(context.Background(), conn, scale)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		got = append(got, cases.Format(c.Name, r))
	}
	if want, have := mask(block), mask(strings.Join(got, "\n")); want != have {
		t.Errorf("README output is stale.\n--- README (masked)\n%s\n--- this run (masked)\n%s", want, have)
	}
}

var volatile = regexp.MustCompile(`\d+(\.\d+)? (ms|buffers|kB|MB)\b|\d+(\.\d+)?x\b`)

func mask(s string) string {
	return strings.TrimSpace(volatile.ReplaceAllString(s, "#"))
}
