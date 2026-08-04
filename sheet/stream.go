package sheet

import (
	"context"
	"fmt"

	"github.com/xraph/dql/internal/rowops"
)

// RowSource is a forward-only cursor over rows.
//
// Defined here rather than in the pipe package because the sheet is what
// consumes one; pipe aliases this type so a host implementing a source has one
// contract to satisfy, not two structurally identical ones.
//
// The usual iteration shape applies: Next until it returns false, then check
// Err. Close is the caller's responsibility and is safe to call more than once.
type RowSource interface {
	Next() bool
	Row() rowops.Row
	Err() error
	Close() error

	// Truncated reports whether iteration stopped at a cap rather than
	// exhausting the underlying result.
	//
	// This is the property streaming exists to provide. A sheet that drained a
	// complete source knows its reduces span every row that matched, which is
	// what lets an aggregate be delegated to the database and still agree with
	// the in-memory answer. Valid once Next has returned false.
	Truncated() bool
}

// sourceCompleteKey carries the "the rows are every matching row" signal from
// whoever drained the source to the sheet that needs it.
type sourceCompleteKey struct{}

// WithSourceComplete records whether the rows a sheet is about to see are the
// complete match or a prefix cut short by a cap.
//
// A caller that materialised rows itself and knows nothing about their
// provenance should not call this: absent means unknown, which Apply treats as
// incomplete. Guessing the other way would let an aggregate be delegated to a
// database on the strength of an assumption.
func WithSourceComplete(ctx context.Context, complete bool) context.Context {
	return context.WithValue(ctx, sourceCompleteKey{}, complete)
}

// sourceCompleteFrom reports the recorded signal, and whether one was recorded.
func sourceCompleteFrom(ctx context.Context) (complete, known bool) {
	v, ok := ctx.Value(sourceCompleteKey{}).(bool)
	return v, ok
}

// SliceSource returns a RowSource over an already-materialised slice, so the
// streaming and non-streaming paths are one implementation rather than two.
func SliceSource(rows []rowops.Row) RowSource {
	return &sliceSource{rows: rows, i: -1}
}

type sliceSource struct {
	rows []rowops.Row
	i    int
}

func (s *sliceSource) Next() bool {
	s.i++
	return s.i < len(s.rows)
}

func (s *sliceSource) Row() rowops.Row {
	if s.i < 0 || s.i >= len(s.rows) {
		return nil
	}
	return s.rows[s.i]
}

func (s *sliceSource) Err() error { return nil }
func (s *sliceSource) Close() error {
	s.i = len(s.rows)
	return nil
}

// Truncated is false: a slice is by definition everything the caller had.
func (s *sliceSource) Truncated() bool { return false }

// ApplyStream drains src and evaluates the sheet over what it yields.
//
// The sheet is row-preserving — its output has its input's cardinality and the
// pipe contract wants that output as rows — so this does not reduce peak
// memory, and it is not meant to. What it provides is provenance: the sheet
// drives the cursor, so Result.Complete records whether the reduces saw every
// matching row rather than a prefix of them.
//
// maxRows caps what will be drawn; zero or less means no cap.
func (s *Sheet) ApplyStream(ctx context.Context, src RowSource, maxRows int) (*Result, error) {
	if src == nil {
		return nil, fmt.Errorf("sheet: nil row source")
	}
	defer func() { _ = src.Close() }()

	var rows []rowops.Row
	if maxRows > 0 {
		rows = make([]rowops.Row, 0, minInt(maxRows, 1024))
	}

	capped := false
	for src.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if maxRows > 0 && len(rows) >= maxRows {
			capped = true
			break
		}
		rows = append(rows, src.Row())
	}
	if err := src.Err(); err != nil {
		return nil, fmt.Errorf("sheet: read source: %w", err)
	}

	// Recorded on the context rather than patched onto the result afterwards,
	// so a sheet reached this way and one reached through Apply read the same
	// signal from the same place.
	return s.Apply(WithSourceComplete(ctx, !capped && !src.Truncated()), rows)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
