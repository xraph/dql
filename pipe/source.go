package pipe

import (
	"context"
	"fmt"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/sheet"
)

// RowSource is a forward-only cursor over rows. Aliased from the sheet package,
// which is what the signal it carries is for, so a host implementing a source
// satisfies one contract rather than two structurally identical ones.
type RowSource = sheet.RowSource

// SliceSource wraps an already-materialised slice as a RowSource.
func SliceSource(rows []dsl.Row) RowSource { return sheet.SliceSource(rows) }

// StreamingExecutor is a ClassicExecutor that can also hand back a cursor
// rather than draining one itself.
//
// Optional in both directions. A host that does not implement it never takes
// the streaming path, and a host that does may decline per query by returning
// a nil StreamResult — which is how a plan that still needs in-memory
// post-processing opts out without the pipe layer having to know why.
type StreamingExecutor interface {
	ClassicExecutor
	ExecuteStream(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*StreamResult, error)
}

// StreamResult is a cursor plus the metadata the executor would otherwise take
// from a materialised QueryResult.
type StreamResult struct {
	Source  RowSource
	Columns []dsl.ColumnInfo
	// Stats is read once the source is drained, since the counts it reports
	// are not known before then. May be nil.
	Stats func() dsl.QueryStats
}

// wantsSourceSignal reports whether any in-memory operator benefits from
// knowing the prefix was complete. Only the sheet does today; asking the
// catalog rather than type-switching keeps that list in one place.
func wantsSourceSignal(plan *PipePlan) bool {
	for _, name := range plan.InMemoryStages {
		if name == "sheet" {
			return true
		}
	}
	return false
}

// streamPrefix draws the pushed prefix through a cursor when that is possible
// and any operator downstream cares about completeness.
//
// The executor drains the cursor rather than handing it to an operator. That
// looks like a missed opportunity and is not one: a sheet is row-preserving,
// so its output carries its input's cardinality and the pipe contract wants
// that output as rows — peak memory is the same either way. What the cursor
// provides is the one thing a drained slice cannot, which is whether the rows
// are every row that matched or a prefix of them. Keeping the drain here also
// leaves the tail's first operator re-runnable, which live replay requires and
// a consumed one-shot source would not be.
//
// Returns ok=false when the streaming path does not apply, leaving the caller
// to run the ordinary materialised path.
func (e *Executor) streamPrefix(
	ctx context.Context,
	plan *PipePlan,
	pushed *dsl.QueryDSL,
	workspaceID, projectID string,
) (rows []dsl.Row, cols []dsl.ColumnInfo, stats dsl.QueryStats, complete, ok bool, err error) {
	if !wantsSourceSignal(plan) {
		return nil, nil, dsl.QueryStats{}, false, false, nil
	}
	se, isStreaming := e.classic.(StreamingExecutor)
	if !isStreaming {
		return nil, nil, dsl.QueryStats{}, false, false, nil
	}

	sr, err := se.ExecuteStream(ctx, pushed, workspaceID, projectID)
	if err != nil {
		return nil, nil, dsl.QueryStats{}, false, false, fmt.Errorf("pipe: classic prefix stream: %w", err)
	}
	if sr == nil || sr.Source == nil {
		// The host declined for this query. Not an error.
		return nil, nil, dsl.QueryStats{}, false, false, nil
	}
	defer func() { _ = sr.Source.Close() }()

	rows = make([]dsl.Row, 0, 1024)
	capped := false
	for sr.Source.Next() {
		if len(rows) >= e.cfg.MaxRows {
			capped = true
			break
		}
		rows = append(rows, sr.Source.Row())
	}
	if srcErr := sr.Source.Err(); srcErr != nil {
		return nil, nil, dsl.QueryStats{}, false, false, fmt.Errorf("pipe: read prefix: %w", srcErr)
	}

	if sr.Stats != nil {
		stats = sr.Stats()
	}
	complete = !capped && !sr.Source.Truncated()
	if !complete {
		// Surfaced the same way an app-source clip is, so a caller reading
		// stats does not have to know which path produced the rows.
		stats.Truncated = true
	}

	return rows, sr.Columns, stats, complete, true, nil
}

// withSourceComplete is how the drained-cursor signal reaches the operators.
// Threaded through context rather than the Operator contract because exactly
// one operator consumes it and widening Apply for that would touch all forty.
func withSourceComplete(ctx context.Context, complete bool) context.Context {
	return sheet.WithSourceComplete(ctx, complete)
}
