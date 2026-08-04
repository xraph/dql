package exec

import (
	"context"
	"fmt"
	"time"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/parser"
	"github.com/xraph/dql/pipe"
	"github.com/xraph/dql/sqlgen"
)

// ExecuteStream serves the pushed prefix as a cursor instead of a slice, so a
// consumer can learn whether it saw every matching row.
//
// Returns a nil result — not an error — when the query is not eligible. That is
// the contract's way of saying "not this one" without the pipe layer needing to
// know the engine's reasons, and the caller falls back to the materialised path.
func (a *classicEngineAdapter) ExecuteStream(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*pipe.StreamResult, error) {
	return a.eng.executeClassicStream(ctx, q, workspaceID, projectID)
}

func (e *Engine) executeClassicStream(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*pipe.StreamResult, error) {
	// applyDefaults and the normalisers mutate, and the caller owns its query.
	qc := *q
	e.applyDefaults(&qc)
	parser.NormalizeAggregateFns(&qc)
	parser.NormalizeOrderDir(&qc)

	plan, err := e.planner.Plan(ctx, &qc, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	// A cursor skips the post-processing stage entirely, so it is only correct
	// when the planner left nothing for it: expression filters, in-memory
	// aggregation, having, sorting, computed columns and pagination all appear
	// in plan.InMemory. Expansion is the same story by another route — it
	// rewrites a materialised result in place.
	if len(plan.InMemory) > 0 || qc.Expand != nil {
		return nil, nil
	}

	// Ask the database for one row past the limit. Reading it is how the cursor
	// tells a page that happens to be full from one that was clipped, which is
	// the whole reason this path exists — the row itself is never yielded.
	limit := 0
	if plan.PushedLimit != nil && *plan.PushedLimit > 0 {
		limit = *plan.PushedLimit
		probe := limit + 1
		plan.PushedLimit = &probe
	}

	sqlStr, params, err := sqlgen.GenerateSQL(plan, e.config.ScopeFor(workspaceID, projectID))
	if err != nil {
		return nil, fmt.Errorf("generate SQL: %w", err)
	}

	sqlRows, err := e.db.Query(ctx, sqlStr, params...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	cols, err := sqlRows.Columns()
	if err != nil {
		_ = sqlRows.Close()
		return nil, fmt.Errorf("get columns: %w", err)
	}

	src := &sqlRowSource{
		rows:  sqlRows,
		cols:  cols,
		limit: limit,
		table: plan.TableName,
		start: time.Now(),
	}
	return &pipe.StreamResult{
		Source:  src,
		Columns: plan.Columns,
		Stats:   src.stats,
	}, nil
}

// sqlRowSource is a forward-only cursor over a live SQLRows.
type sqlRowSource struct {
	rows  SQLRows
	cols  []string
	limit int // 0 means unlimited
	table string
	start time.Time

	n         int
	cur       dsl.Row
	err       error
	truncated bool
	closed    bool
}

func (s *sqlRowSource) Next() bool {
	if s.err != nil || s.closed {
		return false
	}
	if s.limit > 0 && s.n >= s.limit {
		// The probe row. Its presence means the database had more to give, so
		// what the caller received is a prefix rather than the whole match.
		if s.rows.Next() {
			s.truncated = true
		}
		return false
	}
	if !s.rows.Next() {
		return false
	}
	row, err := scanRow(s.rows, s.cols)
	if err != nil {
		s.err = fmt.Errorf("scan row %d: %w", s.n, err)
		return false
	}
	s.cur = row
	s.n++
	return true
}

func (s *sqlRowSource) Row() dsl.Row { return s.cur }

func (s *sqlRowSource) Err() error {
	if s.err != nil {
		return s.err
	}
	return s.rows.Err()
}

func (s *sqlRowSource) Truncated() bool { return s.truncated }

// Close is idempotent: the consumer defers it and the engine may already have
// closed on an error path.
func (s *sqlRowSource) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.rows.Close()
}

func (s *sqlRowSource) stats() dsl.QueryStats {
	return dsl.QueryStats{
		ExecutionMs:  time.Since(s.start).Milliseconds(),
		RowsScanned:  int64(s.n),
		RowsReturned: s.n,
		Sources:      []string{s.table},
		Truncated:    s.truncated,
	}
}
