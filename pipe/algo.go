package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/internal/rowops"
)

// AlgoConfig invokes a registered algorithm from pkg/algorithms.
//
//	Name   — required, e.g. "kmeans" or "minmax_scale".
//	Params — algorithm-specific JSON-decoded parameters. Forwarded verbatim
//	         to the algorithm's Apply method.
type AlgoConfig struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

type algoOp struct {
	cfg      AlgoConfig
	registry AlgorithmRegistry
	liveSafe bool
}

func (o *algoOp) Name() string     { return "algo" }
func (o *algoOp) IsLiveSafe() bool { return o.liveSafe }

func (o *algoOp) Apply(ctx context.Context, in []rowops.Row) ([]rowops.Row, error) {
	out, err := o.registry.Execute(ctx, o.cfg.Name, o.cfg.Params, in)
	if err != nil {
		return nil, fmt.Errorf("algo %s: %w", o.cfg.Name, err)
	}
	return out, nil
}

func algoFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg AlgoConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("algo: decode config: %w", err)
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("algo: name is required")
	}
	if octx == nil || octx.Algorithms == nil {
		return nil, fmt.Errorf("algo: algorithm registry not available")
	}
	liveSafe, ok := octx.Algorithms.LiveSafe(cfg.Name)
	if !ok {
		return nil, fmt.Errorf("algo: unknown algorithm %q", cfg.Name)
	}
	return &algoOp{cfg: cfg, registry: octx.Algorithms, liveSafe: liveSafe}, nil
}

func init() { Register("algo", algoFactory) }
