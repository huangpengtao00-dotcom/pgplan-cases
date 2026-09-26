// Package cases holds reproducible PostgreSQL planner cases. Each case builds
// its own schema, measures, and returns a Result; the tests assert on it and
// `pgplan report` prints it.
package cases

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Result is what one case observed.
type Result struct {
	Case    string
	Facts   []Fact
	Summary string
	// Bool and Num hold the observations the tests assert on.
	Bool map[string]bool
	Num  map[string]float64
}

func newResult(name string) Result {
	return Result{Case: name, Bool: map[string]bool{}, Num: map[string]float64{}}
}

// Fact is one measured line of a result.
type Fact struct {
	Label string
	Value string
}

func (r *Result) add(label, format string, args ...any) {
	r.Facts = append(r.Facts, Fact{label, fmt.Sprintf(format, args...)})
}

// Format renders one case's result as `pgplan` prints it and the README shows it.
func Format(name string, r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", name)
	for _, f := range r.Facts {
		fmt.Fprintf(&b, "  %-28s %s\n", f.Label, f.Value)
	}
	fmt.Fprintf(&b, "  → %s\n", r.Summary)
	return b.String()
}

// Case is one runnable case.
type Case struct {
	Name string
	Run  func(ctx context.Context, conn *pgx.Conn, scale int) (Result, error)
}

// All lists the cases in report order.
var All = []Case{
	{"partial-index-generic-plan", PartialIndexGenericPlan},
	{"candidates-not-backlog", CandidatesNotBacklog},
	{"keyset-needs-a-tiebreaker", KeysetTiebreaker},
}

func schema(ctx context.Context, conn *pgx.Conn, name string) error {
	for _, s := range []string{
		"DROP SCHEMA IF EXISTS " + name + " CASCADE",
		"CREATE SCHEMA " + name,
		"SET search_path TO " + name,
		"SET max_parallel_workers_per_gather = 0", // stable plans across machines
		"SET jit = off",
		"SET TimeZone = 'UTC'",
	} {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

func exec(ctx context.Context, conn *pgx.Conn, stmts ...string) error {
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}
