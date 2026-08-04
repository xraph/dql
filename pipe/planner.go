package pipe

import (
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// PipePlan is the executor-ready plan produced by PlanPipe.
//
// PushedDSL describes the prefix of the pipe that was folded into a classic
// QueryDSL and can be executed by the underlying engine (SQL push-down).
// InMemoryOps is the ordered suite of operators applied to the rows returned
// by the pushed DSL.
//
// InMemoryIDs and InMemoryFroms are parallel to InMemoryOps. InMemoryIDs[i]
// is the optional output name of op i — when non-empty, the executor caches
// op i's output rows under that name so a later op may consume them via
// InMemoryFroms[j] == InMemoryIDs[i]. An empty InMemoryFroms[j] means op j
// consumes the previous op's output (the classic linear chain).
type PipePlan struct {
	PushedDSL   *dsl.QueryDSL
	InMemoryOps []Operator
	// PushedStages lists the op names that were folded into PushedDSL, preserving
	// their original order. Useful for /explain output.
	PushedStages []string
	// InMemoryStages lists the op names that remain as InMemoryOps.
	InMemoryStages []string
	// InMemoryIDs[i] is the user-supplied stage id for InMemoryOps[i] ("" when
	// the stage has no id). Populated even when no later stage references it,
	// so /explain can surface stage names.
	InMemoryIDs []string
	// InMemoryFroms[i] is the stage id whose output InMemoryOps[i] consumes;
	// "" means the previous step's output (linear chain).
	InMemoryFroms []string
}

// PlanPipe walks the pipe stages left-to-right, folding the longest pushable
// prefix into a synthetic classic QueryDSL and leaving the remainder as
// in-memory operators. The input QueryDSL is not mutated.
func PlanPipe(q *dsl.QueryDSL, octx *OpContext) (*PipePlan, error) {
	if q == nil {
		return nil, fmt.Errorf("pipe: nil query")
	}
	if len(q.Pipe) == 0 {
		return nil, fmt.Errorf("pipe: at least one stage is required")
	}

	if err := validateStageGraph(q.Pipe); err != nil {
		return nil, err
	}

	// Stages whose ID is referenced later via `from` must run in-memory so the
	// executor can capture and replay their output rows. Compute the set once
	// up front; the pushability loop below treats these as hard barriers.
	referenced := map[string]bool{}
	for _, s := range q.Pipe {
		if s.From != "" {
			referenced[s.From] = true
		}
	}

	pushed := &dsl.QueryDSL{
		From:       q.From,
		ProjectID:  q.ProjectID,
		Parameters: q.Parameters,
	}
	plan := &PipePlan{PushedDSL: pushed}

	breakAt := -1 // first stage we could not push (exclusive upper bound for the prefix)

	// Track what has already been folded into pushed so later ops obey ordering.
	// groupBy/aggregate both terminate the prefix via `break outer`, so they
	// never need to be tracked as "have" flags here.
	var (
		haveSort   bool
		haveLimit  bool
		haveSkip   bool
		haveSelect bool
	)

outer:
	for i, stage := range q.Pipe {
		// Stage-graph edges are not foldable into the classic prefix:
		//   - `from` makes the stage consume an arbitrary upstream output, not
		//     the linear chain the SQL/Mongo classic engine produces.
		//   - A named stage whose output is referenced later must run in-memory
		//     so the executor can snapshot its rows for the consumers.
		if stage.From != "" || (stage.ID != "" && referenced[stage.ID]) {
			breakAt = i
			break outer
		}
		switch stage.Op {
		case "filter":
			if haveSort || haveLimit || haveSkip {
				breakAt = i
				break outer
			}
			var cfg FilterConfig
			if err := json.Unmarshal(stage.Config, &cfg); err != nil {
				return nil, fmt.Errorf("pipe[%d] filter: %w", i, err)
			}
			// Expression-only filter cannot push; plain column filter can.
			if hasExpr(cfg.Where) {
				breakAt = i
				break outer
			}
			pushed.Where = mergeAnd(pushed.Where, cfg.Where)
			plan.PushedStages = append(plan.PushedStages, "filter")

		case "project":
			if haveSelect || haveLimit || haveSkip {
				breakAt = i
				break outer
			}
			var cfg ProjectConfig
			if err := json.Unmarshal(stage.Config, &cfg); err != nil {
				return nil, fmt.Errorf("pipe[%d] project: %w", i, err)
			}
			if len(cfg.Drop) > 0 || len(cfg.Select) == 0 {
				// Drop-mode and no-op projection stay in memory for v1 simplicity.
				breakAt = i
				break outer
			}
			pushed.Select = append(pushed.Select, cfg.Select...)
			haveSelect = true
			plan.PushedStages = append(plan.PushedStages, "project")

		case "sort":
			if haveLimit || haveSkip {
				breakAt = i
				break outer
			}
			var cfg SortConfig
			if err := json.Unmarshal(stage.Config, &cfg); err != nil {
				return nil, fmt.Errorf("pipe[%d] sort: %w", i, err)
			}
			pushable := true
			for _, ob := range cfg.By {
				if ob.Field == "" {
					pushable = false
					break
				}
			}
			if !pushable {
				breakAt = i
				break outer
			}
			pushed.OrderBy = append(pushed.OrderBy, cfg.By...)
			haveSort = true
			plan.PushedStages = append(plan.PushedStages, "sort")

		case "limit":
			var cfg LimitConfig
			if err := json.Unmarshal(stage.Config, &cfg); err != nil {
				return nil, fmt.Errorf("pipe[%d] limit: %w", i, err)
			}
			n := cfg.N
			pushed.Limit = &n
			haveLimit = true
			plan.PushedStages = append(plan.PushedStages, "limit")

		case "skip":
			if haveLimit {
				breakAt = i
				break outer
			}
			var cfg SkipConfig
			if err := json.Unmarshal(stage.Config, &cfg); err != nil {
				return nil, fmt.Errorf("pipe[%d] skip: %w", i, err)
			}
			n := cfg.N
			pushed.Offset = &n
			haveSkip = true
			plan.PushedStages = append(plan.PushedStages, "skip")

		case "distinct":
			// Pushability of distinct depends on projection shape; keep in-memory for v1.
			breakAt = i
			break outer

		case "groupBy":
			// Pair with the next stage if it's an aggregate.
			if i+1 >= len(q.Pipe) || q.Pipe[i+1].Op != "aggregate" {
				breakAt = i
				break outer
			}
			// Peek ahead — don't advance; the aggregate branch below handles the fold.
			var gb GroupByConfig
			if err := json.Unmarshal(stage.Config, &gb); err != nil {
				return nil, fmt.Errorf("pipe[%d] groupBy: %w", i, err)
			}
			var agg AggregateConfig
			if err := json.Unmarshal(q.Pipe[i+1].Config, &agg); err != nil {
				return nil, fmt.Errorf("pipe[%d] aggregate: %w", i+1, err)
			}
			if !allAggregatesPushable(agg.Aggs) {
				breakAt = i
				break outer
			}
			pushed.GroupBy = gb.Keys
			pushed.Aggregate = normalizeFns(agg.Aggs)
			plan.PushedStages = append(plan.PushedStages, "groupBy", "aggregate")
			// After a pushed aggregate we stop the prefix — later ops operate on
			// aggregate columns and are semantically different.
			breakAt = i + 2
			break outer

		case "aggregate":
			// Stand-alone aggregate (no groupBy): collapses to one row. Only push when
			// all aggregates are pushable — GROUP BY stays empty on the pushed DSL.
			var agg AggregateConfig
			if err := json.Unmarshal(stage.Config, &agg); err != nil {
				return nil, fmt.Errorf("pipe[%d] aggregate: %w", i, err)
			}
			if !allAggregatesPushable(agg.Aggs) {
				breakAt = i
				break outer
			}
			pushed.Aggregate = normalizeFns(agg.Aggs)
			plan.PushedStages = append(plan.PushedStages, "aggregate")
			breakAt = i + 1
			break outer

		default:
			// Any other op (compute, callFunction, callApp, flatten, tap, lookup, ...)
			// breaks the prefix.
			breakAt = i
			break outer
		}
	}

	// Build the in-memory tail.
	tailStart := len(q.Pipe)
	if breakAt >= 0 {
		tailStart = breakAt
	}
	for i := tailStart; i < len(q.Pipe); i++ {
		op, err := Build(q.Pipe[i], octx)
		if err != nil {
			return nil, fmt.Errorf("pipe[%d]: %w", i, err)
		}
		// The prefix is final by now, so an operator that can have work done
		// by the source can be told where to send it.
		if sh, ok := op.(*sheetOp); ok && octx != nil {
			sh.attachDelegate(octx.Classic, pushed)
		}
		plan.InMemoryOps = append(plan.InMemoryOps, op)
		plan.InMemoryStages = append(plan.InMemoryStages, q.Pipe[i].Op)
		plan.InMemoryIDs = append(plan.InMemoryIDs, q.Pipe[i].ID)
		plan.InMemoryFroms = append(plan.InMemoryFroms, q.Pipe[i].From)
	}

	return plan, nil
}

// validateStageGraph checks that stage IDs are valid identifiers and unique,
// and that every `from` reference points at an already-declared stage. The
// executor relies on these invariants to look up cached outputs without
// runtime checks.
func validateStageGraph(stages []dsl.PipeStage) error {
	seen := map[string]bool{}
	for i, s := range stages {
		if s.ID != "" {
			if !isValidStageID(s.ID) {
				return fmt.Errorf("pipe[%d]: id %q is not a valid identifier (letters, digits, underscore; first char letter or underscore)", i, s.ID)
			}
			if seen[s.ID] {
				return fmt.Errorf("pipe[%d]: duplicate stage id %q", i, s.ID)
			}
			seen[s.ID] = true
		}
		if s.From != "" {
			if !seen[s.From] {
				return fmt.Errorf("pipe[%d]: from %q references unknown stage (must point to an earlier stage's id)", i, s.From)
			}
			if s.From == s.ID {
				return fmt.Errorf("pipe[%d]: stage cannot reference its own id %q", i, s.ID)
			}
		}
	}
	return nil
}

// isValidStageID enforces the same identifier shape DTL uses: starts with a
// letter or underscore, then letters / digits / underscores. Keeping it
// strict avoids surprises if stage ids ever surface as DTL variables.
func isValidStageID(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// mergeAnd combines two WHERE clauses under AND semantics.
func mergeAnd(a, b *dsl.WhereClause) *dsl.WhereClause {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &dsl.WhereClause{And: []dsl.WhereClause{*a, *b}}
}

// allAggregatesPushable reports whether every aggregate can be lowered to SQL.
func allAggregatesPushable(aggs []dsl.AggregateClause) bool {
	for _, a := range aggs {
		norm := a
		norm.Fn = upper(a.Fn)
		if !norm.IsPushable() {
			return false
		}
	}
	return true
}

// normalizeFns returns a copy of aggs with Fn uppercased.
func normalizeFns(aggs []dsl.AggregateClause) []dsl.AggregateClause {
	out := make([]dsl.AggregateClause, len(aggs))
	copy(out, aggs)
	for i := range out {
		out[i].Fn = upper(out[i].Fn)
	}
	return out
}

// upper uppercases ASCII letters. Avoids depending on strings here for a hot path.
func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
