package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/expand"
	"github.com/xraph/dql/parser"
	"github.com/xraph/dql/planner"
	"github.com/xraph/dql/processor"
	"github.com/xraph/dql/scope"
	"github.com/xraph/dql/sqlgen"

	"github.com/xraph/dql/pipe"
	"github.com/xraph/dql/sheet"
)

// EngineConfig holds configuration for the query engine.
type EngineConfig struct {
	// ScopeFor maps a caller's partition identifiers to the Scope the planner
	// and generator apply. Supplied by the embedder, because what partitions
	// its data is its own model — see package scope.
	//
	// Required: a nil ScopeFor is refused at construction rather than silently
	// producing SQL that spans every partition.
	ScopeFor func(primary, secondary string) scope.Scope

	DefaultLimit int
	MaxLimit     int
	// PipeDisabled, when true, causes Execute/Explain to reject queries with
	// mode == "pipe". Defaults to false (pipe enabled). Defined as a negative
	// flag so the zero value matches the default-on behaviour.
	PipeDisabled bool
	// PipeMaxStages caps the number of stages in a single pipe query (default 32).
	PipeMaxStages int
	// PipeMaxRows caps the number of rows held in memory between the pushed
	// prefix and the in-memory tail (default 100_000).
	PipeMaxRows int
}

// Engine orchestrates the query execution pipeline:
// parse -> plan -> generate SQL -> execute -> post-process.
type Engine struct {
	db        SQLQuerier
	planner   *planner.Planner
	processor *processor.Processor
	expander  *expand.Expander
	pipeExec  *pipe.Executor
	config    EngineConfig
}

// SetExpander attaches an optional reference expander for _meta enrichment.
func (e *Engine) SetExpander(exp *expand.Expander) { e.expander = exp }

// SetFunctionRegistry wires the function extension's registry into the pipe
// OpContext so the callFunction op can invoke DTL functions.
func (e *Engine) SetFunctionRegistry(reg pipe.FunctionRegistry) {
	if e.pipeExec == nil {
		return
	}
	e.pipeExec.OpContext().Registry = reg
}

// SetAppCaller wires the runtime extension's CallApp into the pipe OpContext
// so the callApp op can invoke managed apps.
func (e *Engine) SetAppCaller(caller pipe.AppCaller) {
	if e.pipeExec == nil {
		return
	}
	e.pipeExec.OpContext().AppCaller = caller
}

// SetFormulaComputer wires the formula extension's Manager into the pipe
// OpContext so compute(kind:formula) can evaluate Excel-style formulas.
func (e *Engine) SetFormulaComputer(c pipe.FormulaComputer) {
	if e.pipeExec == nil {
		return
	}
	e.pipeExec.OpContext().Formula = c
}

// SetExprCompiler wires the host's expression compiler into the pipe
// OpContext. The sheet operator requires it: without one a sheet cannot be
// planned at all, and MissingRequirements reports it as unavailable.
func (e *Engine) SetExprCompiler(c sheet.ExprCompiler) {
	if e.pipeExec == nil {
		return
	}
	e.pipeExec.OpContext().ExprCompiler = c
}

// SetSheetFuncs wires the host's own reduce kernels into the pipe OpContext.
//
// Optional, and not a vocabulary: a sheet may already name any aggregate the
// expression language has. Registering a kernel gives that aggregate a typed
// scan instead of a boxed one, and lets it be delegated to the source when it
// names a SQL spelling. See sheet.Registry.
func (e *Engine) SetSheetFuncs(r *sheet.Registry) {
	if e.pipeExec == nil {
		return
	}
	e.pipeExec.OpContext().SheetFuncs = r
}

// SetAlgorithmRegistry wires the algorithm extension's catalog into the pipe
// OpContext so the algo op can resolve algorithms by name.
func (e *Engine) SetAlgorithmRegistry(reg pipe.AlgorithmRegistry) {
	if e.pipeExec == nil {
		return
	}
	e.pipeExec.OpContext().Algorithms = reg
}

// ExecutePipeDetailed runs a pipe-mode query and returns the result along
// with the plan and a row snapshot taken after the last side-effecting op,
// so live subscriptions can replay the safe tail without re-triggering
// external effects. Errors when q.Mode != "pipe".
func (e *Engine) ExecutePipeDetailed(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*pipe.ExecResult, error) {
	if !q.IsPipeMode() {
		return nil, fmt.Errorf("ExecutePipeDetailed: query is not in pipe mode")
	}
	if e.config.PipeDisabled {
		return nil, fmt.Errorf("pipe-mode queries are disabled in this engine configuration")
	}
	return e.pipeExec.ExecuteDetailed(ctx, q, workspaceID, projectID)
}

// ReExecutePipeFromStage runs the live-safe tail of a pipe plan starting
// from the given primed snapshot. Intended for live-subscription replay.
func (e *Engine) ReExecutePipeFromStage(ctx context.Context, plan *pipe.PipePlan, primedAt int, primedRows []dsl.Row, primedOutputs map[string][]dsl.Row, workspaceID, projectID string) (*dsl.QueryResult, error) {
	return e.pipeExec.ExecuteFromStage(ctx, plan, primedAt, primedRows, primedOutputs, workspaceID, projectID)
}

// PlanPipeOnly builds the pipe plan without executing anything. Used by the
// routing layer to split an app-source pipe into pushed prefix + tail.
func (e *Engine) PlanPipeOnly(q *dsl.QueryDSL) (*pipe.PipePlan, error) {
	if e.config.PipeDisabled {
		return nil, fmt.Errorf("pipe-mode queries are disabled in this engine configuration")
	}
	return pipe.PlanPipe(q, e.pipeExec.OpContext())
}

// ExecutePipeDetailedOnRows runs a planned pipe's tail over an externally
// sourced prefix result (app sources).
func (e *Engine) ExecutePipeDetailedOnRows(ctx context.Context, q *dsl.QueryDSL, plan *pipe.PipePlan, prefix *dsl.QueryResult, workspaceID, projectID string) (*pipe.ExecResult, error) {
	if e.config.PipeDisabled {
		return nil, fmt.Errorf("pipe-mode queries are disabled in this engine configuration")
	}
	return e.pipeExec.ExecuteDetailedOnRows(ctx, q, plan, prefix, workspaceID, projectID)
}

// PipeRowCap exposes the max intermediate row count so callers shaping a
// pushed prefix can apply the same ceiling the executor enforces.
func (e *Engine) PipeRowCap() int { return e.config.PipeMaxRows }

// NewEngine creates a query engine with all dependencies.
func NewEngine(db SQLQuerier, schema planner.SchemaResolver, eval processor.ExprEvaluator, config EngineConfig) *Engine {
	if config.DefaultLimit <= 0 {
		config.DefaultLimit = 100
	}
	if config.MaxLimit <= 0 {
		config.MaxLimit = 10000
	}
	if config.ScopeFor == nil {
		// Refused rather than defaulted. An unscoped default would silently
		// return rows from every partition; an embedder that genuinely wants
		// that passes a ScopeFor returning scope.Scope{}.
		panic("dql/exec: EngineConfig.ScopeFor is required — return scope.Scope{} to run unpartitioned")
	}

	eng := &Engine{
		db:        db,
		planner:   planner.NewPlanner(schema, config.ScopeFor("", "")),
		processor: processor.NewProcessor(eval),
		config:    config,
	}
	eng.pipeExec = pipe.NewExecutor(
		&classicEngineAdapter{eng: eng},
		&pipe.OpContext{Eval: pipeEvalAdapter{eval: eval}},
		pipe.ExecutorConfig{MaxStages: config.PipeMaxStages, MaxRows: config.PipeMaxRows},
	)
	return eng
}

// classicEngineAdapter exposes the classic Execute path to the pipe executor.
type classicEngineAdapter struct{ eng *Engine }

func (a *classicEngineAdapter) Execute(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*dsl.QueryResult, error) {
	return a.eng.executeClassic(ctx, q, workspaceID, projectID)
}

// pipeEvalAdapter is a zero-value-safe adapter: when eval is nil, Eval returns
// a clear error at call time rather than panicking.
type pipeEvalAdapter struct{ eval processor.ExprEvaluator }

func (a pipeEvalAdapter) Eval(ctx context.Context, expr string, row map[string]any) (any, error) {
	if a.eval == nil {
		return nil, fmt.Errorf("expression evaluator not available (function extension missing)")
	}
	return a.eval.Eval(ctx, expr, row)
}

// Execute runs a query and returns the result.
func (e *Engine) Execute(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*dsl.QueryResult, error) {
	if q.IsPipeMode() {
		if e.config.PipeDisabled {
			return nil, fmt.Errorf("pipe-mode queries are disabled in this engine configuration")
		}
		return e.pipeExec.Execute(ctx, q, workspaceID, projectID)
	}
	return e.executeClassic(ctx, q, workspaceID, projectID)
}

// executeClassic runs the classic (non-pipe) path end-to-end.
func (e *Engine) executeClassic(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*dsl.QueryResult, error) {
	start := time.Now()

	// Apply defaults and normalize
	e.applyDefaults(q)
	parser.NormalizeAggregateFns(q)
	parser.NormalizeOrderDir(q)

	// Build execution plan
	plan, err := e.planner.Plan(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	// Generate SQL
	sqlStr, params, err := sqlgen.GenerateSQL(plan, e.config.ScopeFor(workspaceID, projectID))
	if err != nil {
		return nil, fmt.Errorf("generate SQL: %w", err)
	}

	// Execute SQL
	rows, rowsScanned, err := e.executeSQL(ctx, sqlStr, params)
	if err != nil {
		return nil, fmt.Errorf("execute SQL: %w", err)
	}

	// Post-process (computed columns, in-memory filtering, sorting, pagination)
	result, err := e.processor.Process(ctx, plan, q, rows)
	if err != nil {
		return nil, fmt.Errorf("process: %w", err)
	}

	// Enrich reference columns with _meta (opt-in via "expand" in the DSL)
	if q.Expand != nil && e.expander != nil {
		if err := e.expander.Expand(ctx, result, q.Expand, plan.Dataset); err != nil {
			return nil, fmt.Errorf("expand: %w", err)
		}
	}

	// Build stats
	result.Stats = dsl.QueryStats{
		ExecutionMs:  time.Since(start).Milliseconds(),
		RowsScanned:  rowsScanned,
		RowsReturned: len(result.Rows),
		Sources:      []string{plan.TableName},
	}

	// Set total from pre-pagination count if we didn't push limit/offset
	if result.Total == nil {
		t := len(result.Rows)
		result.Total = &t
	}

	// Populate pagination metadata from query limit/offset
	if q.Limit != nil && *q.Limit > 0 {
		limit := *q.Limit
		offset := 0
		if q.Offset != nil {
			offset = *q.Offset
		}
		page := (offset / limit) + 1
		result.Page = &page
		result.PageSize = &limit
		result.HasMore = offset+limit < *result.Total
	}

	return result, nil
}

// Explain returns the query plan without executing.
func (e *Engine) Explain(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*dsl.QueryPlan, error) {
	if q.IsPipeMode() {
		if e.config.PipeDisabled {
			return nil, fmt.Errorf("pipe-mode queries are disabled in this engine configuration")
		}
		return e.pipeExec.Explain(ctx, q, workspaceID, projectID, e.explainClassic)
	}
	return e.explainClassic(ctx, q, workspaceID, projectID)
}

// explainClassic produces a QueryPlan for a non-pipe DSL.
func (e *Engine) explainClassic(ctx context.Context, q *dsl.QueryDSL, workspaceID, projectID string) (*dsl.QueryPlan, error) {
	// Apply defaults and normalize
	e.applyDefaults(q)
	parser.NormalizeAggregateFns(q)
	parser.NormalizeOrderDir(q)

	// Build execution plan
	plan, err := e.planner.Plan(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	// Generate SQL (for display only, not executed)
	_, _, err = sqlgen.GenerateSQL(plan, e.config.ScopeFor(workspaceID, projectID))
	if err != nil {
		return nil, fmt.Errorf("generate SQL: %w", err)
	}

	return plan, nil
}

// applyDefaults sets default values for limit/offset if not specified.
func (e *Engine) applyDefaults(q *dsl.QueryDSL) {
	if q.Limit == nil {
		limit := e.config.DefaultLimit
		q.Limit = &limit
	}
	if *q.Limit > e.config.MaxLimit {
		*q.Limit = e.config.MaxLimit
	}
	if q.Offset == nil {
		offset := 0
		q.Offset = &offset
	}
}

// executeSQL runs the generated SQL and scans results into a slice of maps.
func (e *Engine) executeSQL(ctx context.Context, sqlStr string, params []any) ([]dsl.Row, int64, error) {
	sqlRows, err := e.db.Query(ctx, sqlStr, params...)
	if err != nil {
		return nil, 0, fmt.Errorf("query: %w", err)
	}
	defer func() { _ = sqlRows.Close() }()

	columns, err := sqlRows.Columns()
	if err != nil {
		return nil, 0, fmt.Errorf("get columns: %w", err)
	}

	var rows []dsl.Row
	var scanned int64

	for sqlRows.Next() {
		scanned++
		row, err := scanRow(sqlRows, columns)
		if err != nil {
			return nil, scanned, err
		}
		rows = append(rows, row)
	}

	if err := sqlRows.Err(); err != nil {
		return nil, scanned, fmt.Errorf("iterate rows: %w", err)
	}

	return rows, scanned, nil
}

// scanRow reads the current row into a dsl.Row.
//
// Shared by the materialising and streaming paths deliberately: the two must
// produce identical values for identical input, and two copies of the scan and
// conversion would be free to drift apart on exactly the type edges that are
// hardest to notice.
func scanRow(sqlRows SQLRows, columns []string) (dsl.Row, error) {
	values := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := sqlRows.Scan(ptrs...); err != nil {
		return nil, fmt.Errorf("scan row: %w", err)
	}
	row := make(dsl.Row, len(columns))
	for i, col := range columns {
		row[col] = convertValue(values[i])
	}
	return row, nil
}

// convertValue converts a scanned SQL value to a Go-friendly type.
func convertValue(v any) any {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case []byte:
		// Try to parse as JSON
		var jsonVal any
		if err := json.Unmarshal(val, &jsonVal); err == nil {
			return jsonVal
		}
		return string(val)
	case time.Time:
		return val.Format(time.RFC3339)
	default:
		return val
	}
}
