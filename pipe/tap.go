package pipe

import (
	"context"
	"encoding/json"

	"github.com/xraph/dql/dsl"
)

// TapConfig attaches a debug label. Tap is a pass-through: it records the row
// count against the label for use in /explain output but does not alter rows.
type TapConfig struct {
	Label string `json:"label,omitempty"`
}

type tapOp struct {
	label string
	count int
}

func (o *tapOp) Name() string     { return "tap" }
func (o *tapOp) IsLiveSafe() bool { return true }

func (o *tapOp) Apply(_ context.Context, in []dsl.Row) ([]dsl.Row, error) {
	o.count = len(in)
	return in, nil
}

// Label returns the tap label (used by the executor's stats collector).
func (o *tapOp) Label() string { return o.label }

// Count returns the row count last observed by this tap.
func (o *tapOp) Count() int { return o.count }

func tapFactory(raw json.RawMessage, _ *OpContext) (Operator, error) {
	var cfg TapConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, err
	}
	return &tapOp{label: cfg.Label}, nil
}

func init() { Register("tap", tapFactory) }
