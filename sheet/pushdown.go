package sheet

import (
	"context"
	"fmt"
)

// AggRequest asks for one aggregate to be computed by whatever holds the data,
// rather than by scanning rows here.
type AggRequest struct {
	// As is the formula name the answer belongs to.
	As string
	// Fn is the aggregate's pushdown spelling — see ReduceFunc.PushdownName.
	Fn string
	// Column is the source column to aggregate.
	Column string
}

// ReduceDelegate computes aggregates over the same rows the sheet is
// evaluating.
//
// "The same rows" is the whole contract, and it is not one this package can
// check. A sheet only asks when it knows its input was the complete match (see
// Result.Complete), because that is the condition under which an aggregate
// computed over the underlying source and one computed over the rows in hand
// are the same question.
type ReduceDelegate interface {
	Delegate(ctx context.Context, reqs []AggRequest) (map[string]any, error)
}

// SetReduceDelegate attaches a delegate. Nil disables delegation, which is the
// default: a sheet computes everything itself unless told otherwise.
func (s *Sheet) SetReduceDelegate(d ReduceDelegate) { s.delegate = d }

// delegable reports the reduces that could be computed elsewhere.
//
// Three things must hold. The reduce must resolve to a kernel that names a
// portable aggregate; the column must be a source column rather than one this
// sheet computes, since a delegate has no way to see a value that does not
// exist outside this evaluation; and the caller must have established that the
// rows are the complete match.
func (s *Sheet) delegable(res *Result) []AggRequest {
	if s.delegate == nil || !res.Complete {
		return nil
	}
	var reqs []AggRequest
	for _, f := range s.order {
		k, col, ok := s.kernelFor(f)
		if !ok {
			continue
		}
		if k.PushdownName() == "" || s.isFormula[col] {
			continue
		}
		reqs = append(reqs, AggRequest{As: f.As, Fn: k.PushdownName(), Column: col})
	}
	return reqs
}

// delegateReduces asks the delegate for whatever it can answer, recording the
// results so evalReduce can use them instead of scanning.
//
// A delegate that fails or answers partially is not an error: every reduce it
// does not cover is computed here, which is what the sheet would have done
// anyway. Delegation is an optimisation and may never be the difference
// between a right answer and a wrong one.
func (s *Sheet) delegateReduces(ctx context.Context, run *runState, res *Result) {
	reqs := s.delegable(res)
	if len(reqs) == 0 {
		return
	}
	vals, err := s.delegate.Delegate(ctx, reqs)
	if err != nil || len(vals) == 0 {
		return
	}
	for _, r := range reqs {
		if v, ok := vals[r.As]; ok {
			run.delegated[r.As] = v
		}
	}
}

// checkDelegated verifies a delegated answer is a scalar the sheet can use.
// A delegate returning a slice or a map would otherwise flow into a downstream
// expression as a value no arithmetic accepts, far from where it came from.
func checkDelegated(as string, v any) error {
	switch v.(type) {
	case nil, bool, string, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, float32, float64:
		return nil
	}
	return fmt.Errorf("sheet: delegated reduce %q returned %T, want a scalar", as, v)
}
