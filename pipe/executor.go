package pipe

import (
	"context"
	"fmt"
	"time"

	"github.com/xraph/dql/dsl"
)

// ClassicExecutor runs a non-pipe QueryDSL. The pipe Executor delegates the
// pushed prefix to this. Defined as an interface to break the import cycle
// with the parent engine package and to allow test doubles.
type ClassicExecutor interface {
	Execute(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*dsl.QueryResult, error)
}

// ExecutorConfig holds runtime limits for pipe execution.
type ExecutorConfig struct {
	// MaxStages caps the number of stages a single pipe may contain.
	MaxStages int
	// MaxRows caps the number of rows the executor will hold in memory between
	// the SQL prefix and the in-memory tail.
	MaxRows int
}

// Executor runs a pipe-mode query end-to-end.
type Executor struct {
	classic ClassicExecutor
	octx    *OpContext
	cfg     ExecutorConfig
}

// NewExecutor constructs a pipe executor. The classic executor is used for
// both the pushed SQL prefix AND for secondary queries from lookup ops, so we
// store it on the OpContext as well.
func NewExecutor(classic ClassicExecutor, octx *OpContext, cfg ExecutorConfig) *Executor {
	if cfg.MaxStages <= 0 {
		cfg.MaxStages = 32
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = 100000
	}
	if octx == nil {
		octx = &OpContext{}
	}
	// Only set Classic when the caller hasn't already supplied one (e.g. a
	// wrapper that adds caching, audit, or rate-limiting). The fallback gives
	// lookup ops a sensible default when the caller didn't bother.
	if octx.Classic == nil {
		octx.Classic = classic
	}
	return &Executor{classic: classic, octx: octx, cfg: cfg}
}

// OpContext returns the shared operator context. Callers may mutate its
// service fields (Registry, AppCaller, Formula) at any time; new operators
// constructed after the mutation will see the new values. Operators already
// built cache the service reference they were given.
func (e *Executor) OpContext() *OpContext { return e.octx }

// ExecResult wraps the query result with plan metadata needed for live replay.
type ExecResult struct {
	Result *dsl.QueryResult
	Plan   *PipePlan
	// PrimedRows is the row snapshot taken after the last side-effecting
	// in-memory op, or after the classic prefix when no side-effecting op
	// ran. Live subscriptions use this as the starting point for replay so
	// side-effects aren't re-triggered on every data change.
	PrimedRows []dsl.Row
	// PrimedAt is the in-memory op index (relative to the InMemoryOps slice)
	// that produced PrimedRows. Replay resumes from PrimedAt. When the pipe
	// has no side-effecting op it equals 0 (replay from the start of the tail).
	PrimedAt int
	// PrimedOutputs holds the cached stage outputs (keyed by InMemoryIDs[i])
	// for every named stage that ran before PrimedAt. Live replay restores
	// this map so a downstream stage with `from` can resolve a reference even
	// when the producing stage was skipped during replay.
	PrimedOutputs map[string][]dsl.Row
}

// Execute plans and runs a pipe-mode query. Callers must ensure q.Mode == "pipe".
func (e *Executor) Execute(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*dsl.QueryResult, error) {
	out, err := e.ExecuteDetailed(ctx, q, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	return out.Result, nil
}

// ExecuteDetailed is like Execute but also returns the plan and the row
// snapshot suitable for live-replay priming. Live subscriptions call this
// instead of Execute so they can cache rows at the right stage.
func (e *Executor) ExecuteDetailed(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*ExecResult, error) {
	start := time.Now()

	if len(q.Pipe) > e.cfg.MaxStages {
		return nil, fmt.Errorf("pipe: too many stages (%d > %d)", len(q.Pipe), e.cfg.MaxStages)
	}

	plan, err := PlanPipe(q, e.octx)
	if err != nil {
		return nil, err
	}

	// Execute the pushed prefix via the classic engine. Force Mode="" so the
	// classic engine doesn't loop back into pipe mode.
	pushed := *plan.PushedDSL
	pushed.Mode = ""
	pushed.Pipe = nil

	// When in-memory ops follow the pushed prefix, prevent the classic engine
	// from clipping the prefix to its DefaultLimit (typically 100). The user's
	// pipe likely expects subsequent stages to operate on the full filtered
	// set; the executor's MaxRows check enforces a hard ceiling.
	if len(plan.InMemoryOps) > 0 && pushed.Limit == nil {
		cap := e.cfg.MaxRows
		pushed.Limit = &cap
	}

	// When the tail wants to know whether it saw every matching row and the
	// host can hand over a cursor, draw the prefix through it. The executor
	// still drains it here rather than letting an operator own the cursor:
	// the tail's first operator must stay re-runnable for live replay, which
	// it would not be once it had consumed a one-shot source.
	if rows, cols, stats, complete, ok, err := e.streamPrefix(ctx, plan, &pushed, workspaceID, projectID); err != nil {
		return nil, err
	} else if ok {
		// Recorded only on this path. Absent means unknown, and unknown reads
		// as incomplete downstream, which is the safe direction for anything
		// that would otherwise delegate an aggregate on the strength of it.
		streamCtx := withSourceComplete(ctx, complete)
		return e.applyInMemoryOps(streamCtx, q, plan, rows, cols, stats, start, workspaceID, projectID)
	}

	classicResult, err := e.classic.Execute(ctx, &pushed, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("pipe: classic prefix: %w", err)
	}

	rows := classicResult.Rows
	if len(rows) > e.cfg.MaxRows {
		return nil, fmt.Errorf("pipe: prefix returned %d rows which exceeds the configured limit (%d)", len(rows), e.cfg.MaxRows)
	}

	return e.applyInMemoryOps(ctx, q, plan, rows, classicResult.Columns, classicResult.Stats, start, workspaceID, projectID)
}

// applyInMemoryOps runs the in-memory tail over rows, capturing the primed
// snapshot after the last side-effecting op. baseStats carries the source
// read's telemetry so the final result reports scanned rows, sources, and
// whether the source fetch was truncated.
func (e *Executor) applyInMemoryOps(ctx context.Context, q *dsl.QueryDSL, plan *PipePlan, rows []dsl.Row, classicCols []dsl.ColumnInfo, baseStats dsl.QueryStats, start time.Time, workspaceID, projectID string) (*ExecResult, error) {
	// The primed snapshot is taken *after* the last side-effecting op — or at
	// the very start of the tail if the pipe is pure. We record the stage index
	// each time we cross a non-live-safe op.
	primedRows := cloneRows(rows)
	primedAt := 0
	primedOutputs := map[string][]dsl.Row{}

	// stageOutputs caches every named stage's output so later stages with
	// `from: <id>` can resolve their input. Only populated when a stage has a
	// non-empty InMemoryIDs[i].
	stageOutputs := map[string][]dsl.Row{}

	// pipeStats accumulates per-stage telemetry (rowsIn/rowsOut, time, label)
	// for the in-memory tail. Pushed-prefix stages aren't enumerated here —
	// they're summarised together via plan.PushedStages. When the caller opts
	// out via q.OmitPipeStats we skip collection entirely (no per-op timing,
	// no allocation) rather than collect-then-discard.
	collectStats := !q.OmitPipeStats
	var pipeStats []dsl.PipeStageStat
	if collectStats {
		pipeStats = make([]dsl.PipeStageStat, 0, len(plan.InMemoryOps))
	}

	// Thread workspace/project through ctx so scope-aware ops (compute(formula),
	// lookup, callApp) can resolve them without carrying scope in every config.
	opCtx := withScope(ctx, workspaceID, projectID)

	// Apply in-memory operators in order.
	for i, op := range plan.InMemoryOps {
		input := rows
		if from := stageFromAt(plan, i); from != "" {
			cached, ok := stageOutputs[from]
			if !ok {
				return nil, fmt.Errorf("pipe[%d] %s: from %q has no cached output (the stage may have been skipped or moved)", len(plan.PushedStages)+i, op.Name(), from)
			}
			input = cloneRows(cached)
		}

		var opStart time.Time
		if collectStats {
			opStart = time.Now()
		}
		out, err := op.Apply(opCtx, input)
		if err != nil {
			return nil, fmt.Errorf("pipe[%d] %s: %w", len(plan.PushedStages)+i, op.Name(), err)
		}
		if len(out) > e.cfg.MaxRows {
			return nil, fmt.Errorf("pipe: op %s produced %d rows which exceeds the configured limit (%d)", op.Name(), len(out), e.cfg.MaxRows)
		}
		if id := stageIDAt(plan, i); id != "" {
			stageOutputs[id] = cloneRows(out)
		}

		if collectStats {
			pipeStats = append(pipeStats, dsl.PipeStageStat{
				Index:      len(plan.PushedStages) + i,
				ID:         stageIDAt(plan, i),
				Op:         op.Name(),
				Label:      stageLabel(op),
				RowsIn:     len(input),
				RowsOut:    len(out),
				DurationMs: time.Since(opStart).Milliseconds(),
				Sample:     cloneSample(out, dsl.PipeStageStatSampleSize),
			})
		}

		rows = out

		if !op.IsLiveSafe() {
			// After this op finishes, its output becomes the new "primed" snapshot.
			// Re-execute on a live event will resume from i+1 using this snapshot
			// — including every named output captured so far.
			primedRows = cloneRows(rows)
			primedAt = i + 1
			primedOutputs = cloneOutputs(stageOutputs)
		}
	}

	total := len(rows)
	// Always populate columns. When the prefix ran end-to-end the classic
	// engine already emitted typed ColumnInfo; otherwise we best-effort-derive
	// from the actual rows so clients always have names + ordering for
	// table/viz rendering rather than a useless `null`.
	cols := classicCols
	if len(plan.InMemoryOps) > 0 {
		cols = deriveColumnsFromRows(rows)
	}

	return &ExecResult{
		Result: &dsl.QueryResult{
			Rows:    rows,
			Columns: cols,
			Total:   &total,
			Stats: dsl.QueryStats{
				ExecutionMs:  time.Since(start).Milliseconds(),
				RowsScanned:  baseStats.RowsScanned,
				RowsReturned: len(rows),
				Sources:      baseStats.Sources,
				Pipe:         pipeStats,
				Truncated:    baseStats.Truncated,
			},
		},
		Plan:          plan,
		PrimedRows:    primedRows,
		PrimedAt:      primedAt,
		PrimedOutputs: primedOutputs,
	}, nil
}

// ExecuteDetailedOnRows runs a previously-planned pipe's in-memory tail over
// a result produced outside the classic engine (an app source's pushed
// prefix). The prefix's Columns and Stats (RowsScanned/Sources/Truncated)
// are carried through so a fully-pushed pipe still reports typed columns and
// a capped-fetch truncation flag to the caller. Side-effecting ops DO run
// here — this is a one-shot execution, not a live replay.
func (e *Executor) ExecuteDetailedOnRows(ctx context.Context, q *dsl.QueryDSL, plan *PipePlan, prefix *dsl.QueryResult, workspaceID, projectID string) (*ExecResult, error) {
	start := time.Now()
	if prefix == nil {
		return nil, fmt.Errorf("pipe: nil prefix result")
	}
	if len(q.Pipe) > e.cfg.MaxStages {
		return nil, fmt.Errorf("pipe: too many stages (%d > %d)", len(q.Pipe), e.cfg.MaxStages)
	}
	if len(prefix.Rows) > e.cfg.MaxRows {
		return nil, fmt.Errorf("pipe: source returned %d rows which exceeds the configured limit (%d)", len(prefix.Rows), e.cfg.MaxRows)
	}
	sources := prefix.Stats.Sources
	if len(sources) == 0 {
		sources = []string{"app"}
	}
	stats := dsl.QueryStats{
		RowsScanned: int64(len(prefix.Rows)),
		Sources:     sources,
		Truncated:   prefix.Stats.Truncated,
	}
	return e.applyInMemoryOps(ctx, q, plan, prefix.Rows, prefix.Columns, stats, start, workspaceID, projectID)
}

// stageIDAt returns the optional output id of the in-memory op at index i.
// Falls back to "" when the plan was constructed by a test that didn't set
// the parallel slices — keeps existing tests working.
func stageIDAt(plan *PipePlan, i int) string {
	if i < 0 || i >= len(plan.InMemoryIDs) {
		return ""
	}
	return plan.InMemoryIDs[i]
}

// stageFromAt returns the optional `from` source of the in-memory op at index i.
func stageFromAt(plan *PipePlan, i int) string {
	if i < 0 || i >= len(plan.InMemoryFroms) {
		return ""
	}
	return plan.InMemoryFroms[i]
}

// cloneOutputs makes a shallow copy of the stage-output map. The row slices
// inside are already deep-cloned at insert time, so a top-level copy suffices.
func cloneOutputs(in map[string][]dsl.Row) map[string][]dsl.Row {
	out := make(map[string][]dsl.Row, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ExecuteFromStage runs only the live-safe tail of a previously-planned pipe,
// starting from a snapshot of rows captured after the last side-effecting op.
// Used by live subscriptions to re-execute without re-triggering external
// side effects.
//
// primedOutputs carries the named stage outputs captured up to primedAt so
// downstream stages with `from: <id>` can resolve a reference even though
// the producing stage was skipped during replay. It may be nil for pipes
// that don't use stage ids — the previous-step chain is unaffected.
//
// Any non-live-safe op in the range [primedAt..end) is silently skipped —
// callers (the live manager) are expected to have classified the pipe first
// and only reach this path when DryRun was agreed at subscribe time.
func (e *Executor) ExecuteFromStage(ctx context.Context, plan *PipePlan, primedAt int, primedRows []dsl.Row, primedOutputs map[string][]dsl.Row, workspaceID, projectID string) (*dsl.QueryResult, error) {
	start := time.Now()
	if plan == nil {
		return nil, fmt.Errorf("pipe: nil plan")
	}
	if primedAt < 0 || primedAt > len(plan.InMemoryOps) {
		return nil, fmt.Errorf("pipe: primedAt %d out of range [0,%d]", primedAt, len(plan.InMemoryOps))
	}

	rows := cloneRows(primedRows)
	stageOutputs := cloneOutputs(primedOutputs)
	opCtx := withScope(ctx, workspaceID, projectID)

	for i := primedAt; i < len(plan.InMemoryOps); i++ {
		op := plan.InMemoryOps[i]
		if !op.IsLiveSafe() {
			// Subsequent side-effecting ops stay muted during replay. See
			// comment on the method.
			continue
		}

		input := rows
		if from := stageFromAt(plan, i); from != "" {
			cached, ok := stageOutputs[from]
			if !ok {
				return nil, fmt.Errorf("pipe[%d] %s: from %q has no cached output (replay missing primed snapshot)", len(plan.PushedStages)+i, op.Name(), from)
			}
			input = cloneRows(cached)
		}

		out, err := op.Apply(opCtx, input)
		if err != nil {
			return nil, fmt.Errorf("pipe[%d] %s: %w", len(plan.PushedStages)+i, op.Name(), err)
		}
		if len(out) > e.cfg.MaxRows {
			return nil, fmt.Errorf("pipe: op %s produced %d rows which exceeds the configured limit (%d)", op.Name(), len(out), e.cfg.MaxRows)
		}
		if id := stageIDAt(plan, i); id != "" {
			stageOutputs[id] = cloneRows(out)
		}
		rows = out
	}

	total := len(rows)
	return &dsl.QueryResult{
		Rows:  rows,
		Total: &total,
		Stats: dsl.QueryStats{
			ExecutionMs:  time.Since(start).Milliseconds(),
			RowsReturned: len(rows),
		},
	}, nil
}

// stageLabel returns an operator-specific debug label (currently sourced
// from `tap`). Returns "" when the op doesn't expose one.
func stageLabel(op Operator) string {
	type labelled interface{ Label() string }
	if l, ok := op.(labelled); ok {
		return l.Label()
	}
	return ""
}

// cloneRows makes a shallow copy of the row slice and a shallow copy of each
// row so downstream mutation by replay doesn't corrupt the primed snapshot.
func cloneRows(in []dsl.Row) []dsl.Row {
	out := make([]dsl.Row, len(in))
	for i, r := range in {
		dup := make(dsl.Row, len(r))
		for k, v := range r {
			dup[k] = v
		}
		out[i] = dup
	}
	return out
}

// cloneSample returns up to n deep-cloned rows from the head of rows. The
// clone is independent of the live row stream so subsequent stages can
// mutate rows in place without affecting the captured sample. Returns nil
// when rows is empty so the JSON tag's omitempty kicks in and the field
// disappears from the response.
func cloneSample(rows []dsl.Row, n int) []dsl.Row {
	if len(rows) == 0 || n <= 0 {
		return nil
	}
	if n > len(rows) {
		n = len(rows)
	}
	return cloneRows(rows[:n])
}

// Explain returns the in-memory plan for a pipe query. The classic engine's
// own Explain is invoked for the pushed prefix and its stages are merged.
func (e *Executor) Explain(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string, classicExplain func(context.Context, *dsl.QueryDSL, string, string) (*dsl.QueryPlan, error)) (*dsl.QueryPlan, error) {
	plan, err := PlanPipe(q, e.octx)
	if err != nil {
		return nil, err
	}
	pushed := *plan.PushedDSL
	pushed.Mode = ""
	pushed.Pipe = nil

	qp, err := classicExplain(ctx, &pushed, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("pipe: classic explain: %w", err)
	}
	// Annotate InMemory with the tail stage names so clients can see the full plan.
	qp.InMemory = append(qp.InMemory, plan.InMemoryStages...)
	return qp, nil
}
