package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// TransformConfig computes multiple columns in a single stage. Each entry
// is evaluated in declared order against the row map; later entries can
// reference earlier results because the row is updated in place.
//
//	compute: list of computations to apply (required, ≥ 1)
//	drop:    columns removed after compute runs
//	replace: when true, drop every column not produced by `compute`. Useful
//	         for "reshape into exactly these columns" patterns.
//
// Each compute entry has:
//
//	as:   output column (required)
//	expr: DTL expression (mutually exclusive with from)
//	from: shorthand for plain column copy: `from: "src"` ≡ `expr: "src"`
//
// Live-safe: yes. Not pushable.
type TransformConfig struct {
	Compute []TransformEntry `json:"compute"`
	Drop    []string         `json:"drop,omitempty"`
	Replace bool             `json:"replace,omitempty"`
}

// TransformEntry is one column produced by a transform stage.
type TransformEntry struct {
	As   string `json:"as"`
	Expr string `json:"expr,omitempty"`
	From string `json:"from,omitempty"`
}

type transformOp struct {
	cfg  TransformConfig
	eval ExprEvaluator
}

func (o *transformOp) Name() string     { return "transform" }
func (o *transformOp) IsLiveSafe() bool { return true }

func (o *transformOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	// Pre-compute the set of produced keys (used by replace mode).
	produced := make(map[string]struct{}, len(o.cfg.Compute))
	for _, c := range o.cfg.Compute {
		produced[c.As] = struct{}{}
	}

	for i, row := range in {
		for _, c := range o.cfg.Compute {
			if c.From != "" {
				row[c.As] = row[c.From]
				continue
			}
			v, err := o.eval.Eval(ctx, c.Expr, row)
			if err != nil {
				return nil, fmt.Errorf("transform %q row %d: %w", c.As, i, err)
			}
			row[c.As] = v
		}
		if o.cfg.Replace {
			for k := range row {
				if _, keep := produced[k]; !keep {
					delete(row, k)
				}
			}
		}
		for _, d := range o.cfg.Drop {
			delete(row, d)
		}
	}
	return in, nil
}

func transformFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg TransformConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("transform: decode config: %w", err)
	}
	if len(cfg.Compute) == 0 {
		return nil, fmt.Errorf("transform: compute is required and must be non-empty")
	}
	needsEval := false
	for i, c := range cfg.Compute {
		if c.As == "" {
			return nil, fmt.Errorf("transform.compute[%d]: as is required", i)
		}
		if c.Expr == "" && c.From == "" {
			return nil, fmt.Errorf("transform.compute[%d]: either expr or from is required", i)
		}
		if c.Expr != "" && c.From != "" {
			return nil, fmt.Errorf("transform.compute[%d]: expr and from are mutually exclusive", i)
		}
		if c.Expr != "" {
			needsEval = true
		}
	}
	op := &transformOp{cfg: cfg}
	if needsEval {
		if octx == nil || octx.Eval == nil {
			return nil, fmt.Errorf("transform: expression evaluator not available (function extension missing)")
		}
		op.eval = octx.Eval
	}
	return op, nil
}

func init() { Register("transform", transformFactory) }
