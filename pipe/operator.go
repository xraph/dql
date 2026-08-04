// Package pipe implements the query-extension "pipe mode": an ordered list of
// row-transform operators applied to the output of a source dataset. It sits
// next to the classic field-based engine and is dispatched when the incoming
// QueryDSL has Mode == "pipe".
package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/internal/rowops"
	"github.com/xraph/dql/sheet"
)

// Operator aliases rowops.Operator so downstream code can keep referring to
// pipe.Operator while the interface itself lives in a shared package.
type Operator = rowops.Operator

// ExprEvaluator matches processor.ExprEvaluator. Re-declared locally to avoid an
// import cycle back into the parent engine package.
type ExprEvaluator interface {
	Eval(ctx context.Context, expr string, row map[string]any) (any, error)
}

// FunctionRegistry is the subset of the function extension's registry used by
// the callFunction op. The interface is narrow so tests can supply fakes.
type FunctionRegistry interface {
	Execute(ctx context.Context, fullName string, args map[string]any) (any, error)
}

// AppCaller invokes a managed app. It mirrors runtime.Manager.CallApp so the
// wiring in the query extension is a direct method-value passthrough.
type AppCaller interface {
	CallApp(ctx context.Context, appID, method string, payload map[string]any) (map[string]any, error)
}

// FormulaComputer is the narrow view of the formula extension's Manager that
// the compute(kind:formula) op needs. One formula per stage is enough for v1;
// multi-formula support can grow into this interface later.
type FormulaComputer interface {
	ComputeOne(ctx context.Context, workspaceID, projectID, as, expression string, rows []map[string]any) ([]map[string]any, error)
}

// AlgorithmRegistry is the narrow view of pkg/algorithms.Registry used by the
// generic `algo` operator. Returning the algorithm itself would require a
// shared concrete type, so the interface answers the two questions the op
// actually needs at build and execute time.
type AlgorithmRegistry interface {
	// LiveSafe reports whether the algorithm is registered and whether it is
	// pure / deterministic. ok is false when the algorithm is unknown.
	LiveSafe(name string) (liveSafe bool, ok bool)
	// Execute runs the algorithm against the provided rows.
	Execute(ctx context.Context, name string, params map[string]any, rows []rowops.Row) ([]rowops.Row, error)
}

// OpContext carries the services an operator may need to build itself. Fields
// are nullable — when a factory requires a missing service it must return a
// clear error at Build time so callers learn at plan time, not row time.
type OpContext struct {
	Eval       ExprEvaluator
	Registry   FunctionRegistry
	AppCaller  AppCaller
	Formula    FormulaComputer
	Classic    ClassicExecutor
	Algorithms AlgorithmRegistry
	// ExprCompiler prepares an expression once and reports what it references.
	// Required by the sheet operator, whose dependency resolution rests on
	// that analysis; see sheet.ExprCompiler for why it cannot live here.
	ExprCompiler sheet.ExprCompiler
	// SheetFuncs holds the host's own reduce kernels for the sheet operator.
	// Nil means the built-in set only, which is the portable one — see
	// sheet.Registry for why this is per-context rather than global.
	SheetFuncs *sheet.Registry
}

// Factory builds an Operator from its raw per-stage config.
type Factory func(cfg json.RawMessage, octx *OpContext) (Operator, error)

var registry = map[string]Factory{}

// Register makes a Factory available under a stage op name. Callers should
// register in init(); double-registration panics to catch programmer errors.
func Register(name string, f Factory) {
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("pipe: op %q already registered", name))
	}
	registry[name] = f
}

// Known returns true when a factory is registered for the given op name.
func Known(name string) bool {
	_, ok := registry[name]
	return ok
}

// RegisteredOps returns a snapshot of every registered op name. Useful for
// error messages and tests.
func RegisteredOps() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// Build turns a single parsed stage into an Operator, decoding its config.
func Build(stage dsl.PipeStage, octx *OpContext) (Operator, error) {
	f, ok := registry[stage.Op]
	if !ok {
		return nil, fmt.Errorf("unknown pipe op %q", stage.Op)
	}
	return f(stage.Config, octx)
}

// ValidateStages confirms every stage's op is known and its config decodes
// cleanly. It does NOT execute any operators.
func ValidateStages(stages []dsl.PipeStage, octx *OpContext) error {
	for i, s := range stages {
		if s.Op == "" {
			return fmt.Errorf("pipe[%d]: op is required", i)
		}
		if _, err := Build(s, octx); err != nil {
			return fmt.Errorf("pipe[%d]: %w", i, err)
		}
	}
	return nil
}

// decodeConfig unmarshals a stage's raw config into a typed struct. Factories
// use this rather than calling json.Unmarshal directly so that an empty config
// (valid for ops like `tap` with no fields) doesn't error.
func decodeConfig(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
