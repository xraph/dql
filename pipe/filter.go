package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// FilterConfig is the JSON body of a filter stage.
type FilterConfig struct {
	Where *dsl.WhereClause `json:"where"`
}

type filterOp struct {
	cfg  FilterConfig
	eval ExprEvaluator
}

func (o *filterOp) Name() string     { return "filter" }
func (o *filterOp) IsLiveSafe() bool { return true }

func (o *filterOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	if o.cfg.Where == nil {
		return in, nil
	}
	out := make([]dsl.Row, 0, len(in))
	for _, row := range in {
		ok, err := evalWhere(ctx, o.cfg.Where, row, o.eval)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func filterFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg FilterConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("filter: decode config: %w", err)
	}
	if cfg.Where == nil {
		return nil, fmt.Errorf("filter: where is required")
	}
	if hasExpr(cfg.Where) && (octx == nil || octx.Eval == nil) {
		return nil, fmt.Errorf("filter: expression evaluator not available (function extension missing)")
	}
	return &filterOp{cfg: cfg, eval: evalOf(octx)}, nil
}

func init() { Register("filter", filterFactory) }

// hasExpr reports whether a WHERE tree contains any DTL expression nodes.
func hasExpr(w *dsl.WhereClause) bool {
	if w == nil {
		return false
	}
	if w.IsExpr() {
		return true
	}
	for i := range w.And {
		if hasExpr(&w.And[i]) {
			return true
		}
	}
	for i := range w.Or {
		if hasExpr(&w.Or[i]) {
			return true
		}
	}
	return hasExpr(w.Not)
}

func evalOf(octx *OpContext) ExprEvaluator {
	if octx == nil {
		return nil
	}
	return octx.Eval
}
