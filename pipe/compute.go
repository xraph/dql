package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// ComputeConfig adds one column to each row. Two kinds are supported:
//
//	kind:"expr"    (default) — evaluates a DTL expression via the function
//	                            registry's ExecuteInline for each row.
//	kind:"formula" — evaluates an Excel-style formula via the formula
//	                 extension's Manager.Compute. Useful for complex formulas
//	                 with cross-row references (SUM, AVG, VLOOKUP, ...).
//
// Workspace/project scope for the formula kind is supplied at execute time
// through a context value set by the executor.
type ComputeConfig struct {
	As      string `json:"as"`
	Expr    string `json:"expr,omitempty"`
	Formula string `json:"formula,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// computeExprOp is the DTL-expression variant of compute.
type computeExprOp struct {
	cfg  ComputeConfig
	eval ExprEvaluator
}

func (o *computeExprOp) Name() string     { return "compute" }
func (o *computeExprOp) IsLiveSafe() bool { return true }

func (o *computeExprOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	for i, row := range in {
		val, err := o.eval.Eval(ctx, o.cfg.Expr, row)
		if err != nil {
			return nil, fmt.Errorf("compute %q row %d: %w", o.cfg.As, i, err)
		}
		row[o.cfg.As] = val
	}
	return in, nil
}

// computeFormulaOp is the Excel-formula variant. It defers workspace/project
// resolution to execute time via scope context values (see scope.go).
type computeFormulaOp struct {
	cfg      ComputeConfig
	computer FormulaComputer
}

func (o *computeFormulaOp) Name() string     { return "compute" }
func (o *computeFormulaOp) IsLiveSafe() bool { return true }

func (o *computeFormulaOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	ws, proj := scopeFrom(ctx)
	// Deep-clone the input row maps before passing to the formula manager.
	// The formula manager mutates rows in place to add the result column;
	// without this clone the caller's input rows would also gain the column,
	// which leaks across ops (especially live replay where primed rows must
	// stay clean).
	rows := make([]map[string]any, len(in))
	for i, r := range in {
		dup := make(map[string]any, len(r)+1)
		for k, v := range r {
			dup[k] = v
		}
		rows[i] = dup
	}
	out, err := o.computer.ComputeOne(ctx, ws, proj, o.cfg.As, o.cfg.Formula, rows)
	if err != nil {
		return nil, fmt.Errorf("compute(formula) %q: %w", o.cfg.As, err)
	}
	result := make([]dsl.Row, len(out))
	copy(result, out)
	return result, nil
}

func computeFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg ComputeConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("compute: decode config: %w", err)
	}
	if cfg.As == "" {
		return nil, fmt.Errorf("compute: as is required")
	}
	kind := cfg.Kind
	if kind == "" {
		kind = "expr"
	}
	switch kind {
	case "expr":
		if cfg.Expr == "" {
			return nil, fmt.Errorf("compute: expr is required when kind=expr")
		}
		if octx == nil || octx.Eval == nil {
			return nil, fmt.Errorf("compute: expression evaluator not available (function extension missing)")
		}
		return &computeExprOp{cfg: cfg, eval: octx.Eval}, nil
	case "formula":
		if cfg.Formula == "" {
			return nil, fmt.Errorf("compute: formula is required when kind=formula")
		}
		if octx == nil || octx.Formula == nil {
			return nil, fmt.Errorf("compute: formula computer not available (formula extension missing)")
		}
		return &computeFormulaOp{cfg: cfg, computer: octx.Formula}, nil
	default:
		return nil, fmt.Errorf("compute: unknown kind %q", kind)
	}
}

func init() { Register("compute", computeFactory) }
