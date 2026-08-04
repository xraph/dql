package pipe

import (
	"context"
	"fmt"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/sheet"
)

// reduceDelegate answers a sheet's aggregate requests with one classic query
// derived from the prefix the sheet's own rows came from.
type reduceDelegate struct {
	classic ClassicExecutor
	// pushed is the prefix DSL, taken from the plan rather than rebuilt. Two
	// generators for the same row set would be two things to keep in step, and
	// an aggregate spanning a different set than the rows it is divided into
	// is a wrong answer that looks right.
	pushed *dsl.QueryDSL
}

// eligible reports whether the prefix is one an aggregate can be derived from.
//
// The conditions are all about the aggregate covering the same rows:
//
//   - An OFFSET applies to result rows, so `SELECT sum(x) … OFFSET n` discards
//     aggregate rows rather than skipping input rows. There is no spelling of
//     it that means what the prefix meant.
//   - A prefix that already groups or aggregates does not hand the sheet table
//     columns at all; its output columns exist only in that result.
//
// A LIMIT is deliberately not on this list. The caller only delegates when the
// prefix was complete, which is precisely the case where the limit did not
// bind — so an aggregate without one spans the same rows. That is what makes
// this possible without a subquery the document format cannot express.
func (d *reduceDelegate) eligible() bool {
	if d.pushed == nil {
		return false
	}
	if d.pushed.Offset != nil && *d.pushed.Offset > 0 {
		return false
	}
	if len(d.pushed.GroupBy) > 0 || len(d.pushed.Aggregate) > 0 {
		return false
	}
	return true
}

func (d *reduceDelegate) Delegate(ctx context.Context, reqs []sheet.AggRequest) (map[string]any, error) {
	if len(reqs) == 0 || d.classic == nil || !d.eligible() {
		return nil, nil
	}

	// Every clause of the prefix is carried over except the projection and the
	// paging, so the aggregate sees the same table, the same predicate and the
	// same scope. Ordering is dropped because it cannot change an aggregate and
	// only costs a sort.
	q := *d.pushed
	q.Mode = ""
	q.Pipe = nil
	q.Select = nil
	q.OrderBy = nil
	q.Offset = nil

	// An aggregate with no grouping is exactly one row, so ask for one.
	//
	// Stated rather than left to the engine's default limit. A LIMIT on an
	// ungrouped aggregate clips result rows and cannot change the aggregate,
	// so any value at least one is equally correct here — but that is a
	// property of there being no GROUP BY, which eligible() enforces above and
	// which nothing else in this file would reveal.
	one := 1
	q.Limit = &one

	q.Aggregate = make([]dsl.AggregateClause, 0, len(reqs))
	for i, r := range reqs {
		q.Aggregate = append(q.Aggregate, dsl.AggregateClause{
			Fn:    r.Fn,
			Field: r.Column,
			As:    aggAlias(i),
		})
	}

	// Scope comes from the context the executor threaded, the same source the
	// other host-backed operators read it from — the delegate is built at plan
	// time, before there is a caller to attribute it to.
	workspaceID, projectID := scopeFrom(ctx)
	res, err := d.classic.Execute(ctx, &q, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("pipe: delegate reduces: %w", err)
	}
	if res == nil || len(res.Rows) != 1 {
		// An aggregate with no grouping is one row. Anything else means the
		// query did not mean what this assumed, and guessing from it would put
		// a wrong scalar into the sheet.
		return nil, nil
	}

	out := make(map[string]any, len(reqs))
	for i, r := range reqs {
		v, ok := res.Rows[0][aggAlias(i)]
		if !ok {
			continue
		}
		out[r.As] = v
	}
	return out, nil
}

// aggAlias names the i'th aggregate. Positional rather than derived from the
// formula name, which is the user's and need not be a legal SQL identifier.
func aggAlias(i int) string { return fmt.Sprintf("__agg_%d", i) }
