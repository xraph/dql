// Package rowops defines a minimal shared contract for operators that transform
// a batch of rows. It is intentionally tiny so it can be adopted anywhere
// without dragging in a caller's internal types.
//
// Vendored so the query library carries no host dependency: the contract is
// 25 lines and a shared module for it would be more coupling than it removes.
package rowops

import "context"

// Row is a single result row represented as a field map.
// Aliased (not a distinct type) so existing code using map[string]any keeps working.
type Row = map[string]any

// Operator transforms an input batch of rows into an output batch.
// Implementations must be safe to call without shared mutable state across calls.
type Operator interface {
	// Name identifies the operator for logging and error messages. Should be stable.
	Name() string
	// Apply transforms input rows. Implementations may return the input slice
	// directly when no transformation is needed; callers must not assume a new
	// slice is allocated.
	Apply(ctx context.Context, in []Row) ([]Row, error)
	// IsLiveSafe reports whether the operator is pure and may run inside a live
	// subscription's re-execute loop. Side-effecting ops (external HTTP, writes,
	// non-deterministic reads) must return false.
	IsLiveSafe() bool
}
