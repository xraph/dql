package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// BranchConfig routes each input row to one of two sub-pipes based on a
// per-row predicate.
//
//	When  — DTL expression evaluated against the row; rows for which the
//	        expression returns truthy go through the Then sub-pipe, others
//	        through the Else sub-pipe (or pass through unchanged when Else
//	        is empty).
//	Then  — sequence of stages applied to truthy rows.
//	Else  — sequence of stages applied to falsy rows. Optional.
//
// Output rows are concatenated in the order: branch outputs preserved per
// bucket, then-bucket first, else-bucket second. Use a downstream sort if
// stable input ordering matters.
type BranchConfig struct {
	When string          `json:"when"`
	Then []dsl.PipeStage `json:"then"`
	Else []dsl.PipeStage `json:"else,omitempty"`
}

type branchOp struct {
	cfg     BranchConfig
	eval    ExprEvaluator
	thenOps []Operator
	elseOps []Operator
}

func (o *branchOp) Name() string { return "branch" }

func (o *branchOp) IsLiveSafe() bool {
	for _, op := range o.thenOps {
		if !op.IsLiveSafe() {
			return false
		}
	}
	for _, op := range o.elseOps {
		if !op.IsLiveSafe() {
			return false
		}
	}
	return true
}

func (o *branchOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	thenIn := make([]dsl.Row, 0)
	elseIn := make([]dsl.Row, 0)
	for i, row := range in {
		v, err := o.eval.Eval(ctx, o.cfg.When, row)
		if err != nil {
			return nil, fmt.Errorf("branch: row %d: %w", i, err)
		}
		if toBool(v) {
			thenIn = append(thenIn, row)
		} else {
			elseIn = append(elseIn, row)
		}
	}
	thenOut, err := applyChain(ctx, o.thenOps, thenIn)
	if err != nil {
		return nil, fmt.Errorf("branch.then: %w", err)
	}
	elseOut, err := applyChain(ctx, o.elseOps, elseIn)
	if err != nil {
		return nil, fmt.Errorf("branch.else: %w", err)
	}
	out := make([]dsl.Row, 0, len(thenOut)+len(elseOut))
	out = append(out, thenOut...)
	out = append(out, elseOut...)
	return out, nil
}

// applyChain runs a slice of operators in sequence.
func applyChain(ctx context.Context, ops []Operator, in []dsl.Row) ([]dsl.Row, error) {
	rows := in
	for i, op := range ops {
		var err error
		rows, err = op.Apply(ctx, rows)
		if err != nil {
			return nil, fmt.Errorf("[%d] %s: %w", i, op.Name(), err)
		}
	}
	return rows, nil
}

func branchFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg BranchConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("branch: decode config: %w", err)
	}
	if cfg.When == "" {
		return nil, fmt.Errorf("branch: when is required")
	}
	if octx == nil || octx.Eval == nil {
		return nil, fmt.Errorf("branch: expression evaluator not available")
	}
	if len(cfg.Then) == 0 {
		return nil, fmt.Errorf("branch: then is required and must be non-empty")
	}

	thenOps, err := buildOps(cfg.Then, octx)
	if err != nil {
		return nil, fmt.Errorf("branch.then: %w", err)
	}
	elseOps, err := buildOps(cfg.Else, octx)
	if err != nil {
		return nil, fmt.Errorf("branch.else: %w", err)
	}
	return &branchOp{
		cfg:     cfg,
		eval:    octx.Eval,
		thenOps: thenOps,
		elseOps: elseOps,
	}, nil
}

// buildOps materialises a list of stages into operators.
func buildOps(stages []dsl.PipeStage, octx *OpContext) ([]Operator, error) {
	out := make([]Operator, 0, len(stages))
	for i, s := range stages {
		op, err := Build(s, octx)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		out = append(out, op)
	}
	return out, nil
}

func init() { Register("branch", branchFactory) }
