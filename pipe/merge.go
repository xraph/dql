package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
)

// MergeConfig fans out the input row stream to N parallel sub-pipes and
// concatenates their outputs.
//
//	Sources — list of sub-pipes. Each receives a *clone* of the upstream
//	          input rows so they can mutate independently. Outputs are
//	          appended in declared order.
//
// Useful for "tag-and-union" patterns where the same source rows need to
// be transformed two different ways and emitted as one stream.
type MergeConfig struct {
	Sources []MergeSource `json:"sources"`
}

// MergeSource wraps a sub-pipe.
type MergeSource struct {
	Pipe []dsl.PipeStage `json:"pipe"`
}

type mergeOp struct {
	cfg    MergeConfig
	subOps [][]Operator
}

func (o *mergeOp) Name() string { return "merge" }

func (o *mergeOp) IsLiveSafe() bool {
	for _, sub := range o.subOps {
		for _, op := range sub {
			if !op.IsLiveSafe() {
				return false
			}
		}
	}
	return true
}

func (o *mergeOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	out := make([]dsl.Row, 0, len(in)*len(o.subOps))
	for i, sub := range o.subOps {
		// Clone the input so each sub-pipe starts fresh — they'd otherwise
		// share row maps and one branch's mutation would leak into the next.
		input := cloneRows(in)
		rows, err := applyChain(ctx, sub, input)
		if err != nil {
			return nil, fmt.Errorf("merge.sources[%d]: %w", i, err)
		}
		out = append(out, rows...)
	}
	return out, nil
}

func mergeFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg MergeConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("merge: decode config: %w", err)
	}
	if len(cfg.Sources) == 0 {
		return nil, fmt.Errorf("merge: at least one source is required")
	}
	subs := make([][]Operator, len(cfg.Sources))
	for i, src := range cfg.Sources {
		if len(src.Pipe) == 0 {
			return nil, fmt.Errorf("merge.sources[%d]: pipe is required and must be non-empty", i)
		}
		ops, err := buildOps(src.Pipe, octx)
		if err != nil {
			return nil, fmt.Errorf("merge.sources[%d]: %w", i, err)
		}
		subs[i] = ops
	}
	return &mergeOp{cfg: cfg, subOps: subs}, nil
}

func init() { Register("merge", mergeFactory) }
