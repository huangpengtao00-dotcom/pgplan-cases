// Package plan runs EXPLAIN (FORMAT JSON) and exposes the plan tree, so tests
// can assert on what the planner chose instead of on how long it happened to take.
package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Node is one plan node. Only the fields the cases read are decoded.
type Node struct {
	NodeType            string  `json:"Node Type"`
	RelationName        string  `json:"Relation Name"`
	IndexName           string  `json:"Index Name"`
	IndexCond           string  `json:"Index Cond"`
	Filter              string  `json:"Filter"`
	ActualRows          float64 `json:"Actual Rows"`
	ActualLoops         float64 `json:"Actual Loops"`
	RowsRemovedByFilter float64 `json:"Rows Removed by Filter"`
	SharedHitBlocks     int64   `json:"Shared Hit Blocks"`
	SharedReadBlocks    int64   `json:"Shared Read Blocks"`
	Plans               []Node  `json:"Plans"`
}

// Explained is the top of an EXPLAIN result.
type Explained struct {
	Plan          Node    `json:"Plan"`
	ExecutionTime float64 `json:"Execution Time"`
	PlanningTime  float64 `json:"Planning Time"`
}

// Querier is satisfied by *pgx.Conn and pgx.Tx.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Explain runs EXPLAIN with the given options ("ANALYZE, BUFFERS" or "") on
// stmt, which may be a SELECT or an EXECUTE of a prepared statement.
func Explain(ctx context.Context, q Querier, options, stmt string, args ...any) (Explained, error) {
	opts := "FORMAT JSON"
	if options != "" {
		opts = options + ", " + opts
	}
	var raw []byte
	if err := q.QueryRow(ctx, "EXPLAIN ("+opts+") "+stmt, args...).Scan(&raw); err != nil {
		return Explained{}, fmt.Errorf("explain %q: %w", stmt, err)
	}
	var out []Explained
	if err := json.Unmarshal(raw, &out); err != nil {
		return Explained{}, err
	}
	if len(out) != 1 {
		return Explained{}, fmt.Errorf("explain returned %d plans", len(out))
	}
	return out[0], nil
}

// Walk visits every node depth-first.
func (n Node) Walk(fn func(Node)) {
	fn(n)
	for _, c := range n.Plans {
		c.Walk(fn)
	}
}

// Uses reports whether any node scans the named index.
func (n Node) Uses(index string) bool {
	found := false
	n.Walk(func(m Node) {
		if m.IndexName == index {
			found = true
		}
	})
	return found
}

// Scans lists "NodeType(relation or index)" for every scan node.
func (n Node) Scans() []string {
	var out []string
	n.Walk(func(m Node) {
		if strings.HasSuffix(m.NodeType, "Scan") {
			target := m.IndexName
			if target == "" {
				target = m.RelationName
			}
			out = append(out, m.NodeType+"("+target+")")
		}
	})
	return out
}

// RowsRemoved sums "Rows Removed by Filter" across the tree, per loop.
func (n Node) RowsRemoved() float64 {
	total := 0.0
	n.Walk(func(m Node) {
		loops := m.ActualLoops
		if loops == 0 {
			loops = 1
		}
		total += m.RowsRemovedByFilter * loops
	})
	return total
}

// Buffers sums shared hit and read blocks of the root node (which includes children).
func (n Node) Buffers() int64 { return n.SharedHitBlocks + n.SharedReadBlocks }
