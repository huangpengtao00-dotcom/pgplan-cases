// Command pgplan runs every case against $PGPLAN_DSN and prints what it measured.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/huangpengtao00-dotcom/pgplan-cases/cases"
)

func main() {
	scale := flag.Int("scale", 1000, "row multiplier (1000 = the sizes used in the README)")
	flag.Parse()
	dsn := os.Getenv("PGPLAN_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "set PGPLAN_DSN, e.g. postgres://postgres@localhost:5432/postgres")
		os.Exit(2)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close(ctx)
	var version string
	if err := conn.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		fmt.Fprintln(os.Stderr, "server_version:", err)
		os.Exit(1)
	}
	fmt.Printf("PostgreSQL %s · scale %d\n", version, *scale)
	failed := false
	for _, c := range cases.All {
		r, err := c.Run(ctx, conn, *scale)
		if err != nil {
			fmt.Printf("\n%s\n  error: %v\n", c.Name, err)
			failed = true
			continue
		}
		fmt.Printf("\n%s", cases.Format(c.Name, r))
	}
	if failed {
		os.Exit(1)
	}
}
