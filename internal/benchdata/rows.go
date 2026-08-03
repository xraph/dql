// Package benchdata generates deterministic row fixtures for benchmarks.
//
// Every generator is seeded from a constant, so repeated runs — and runs on
// different commits — produce byte-identical rows. That is a precondition for
// comparing benchmark results at all: a clock-seeded generator would make each
// run measure slightly different work, and the resulting numbers would look
// plausible while meaning nothing.
//
// Nothing outside a _test.go file imports this package, so it never enters the
// build graph of code depending on the dql module.
package benchdata

import (
	"fmt"
	"math/rand"
	"time"
)

// defaultSeed is fixed deliberately. See the package comment.
const defaultSeed int64 = 0x5C0FFEE

// epoch anchors created_at so the time-based operators see a stable window.
var epoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

var statuses = []string{"open", "closed", "pending", "blocked", "archived"}

// Sizes is the row-count sweep every row-engine benchmark uses.
//
// 100 isolates per-call overhead, 1000 is the realistic middle, and 10000 is
// where an accidental quadratic becomes unmistakable — 100x the work of the
// 1000 case, which stands out even through CI runner noise.
func Sizes() []int { return []int{100, 1000, 10000} }

// Rows generates n rows with the given grouping cardinality, using the default
// seed. cardinality controls how many distinct assignee values appear, which is
// what separates a cheap group-by from an expensive one.
func Rows(n, cardinality int) []map[string]any {
	return RowsSeeded(n, cardinality, defaultSeed)
}

// RowsSeeded is Rows with an explicit seed, for benchmarks needing two
// independent-looking datasets (joins, set operations).
func RowsSeeded(n, cardinality int, seed int64) []map[string]any {
	if cardinality < 1 {
		cardinality = 1
	}
	// #nosec G404 -- deterministic fixture data, never security-sensitive.
	rng := rand.New(rand.NewSource(seed))
	out := make([]map[string]any, n)
	for i := range out {
		grp := i % cardinality
		out[i] = map[string]any{
			"id":         i,
			"status":     statuses[grp%len(statuses)],
			"assignee":   fmt.Sprintf("user-%d", grp),
			"score":      rng.Float64() * 100,
			"created_at": epoch.Add(time.Duration(i) * time.Minute),
			"tags":       []string{"a", "b"},
			"meta":       map[string]any{"k": grp},
		}
	}
	return out
}
