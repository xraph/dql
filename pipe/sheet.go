package pipe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xraph/dql/dsl"
	"github.com/xraph/dql/sheet"
)

// SheetConfig declares a set of named formulas resolved by what they reference
// rather than by the order they are written in.
type SheetConfig struct {
	Formulas []sheet.Formula `json:"formulas"`
	OnError  string          `json:"onError,omitempty"`
	// ColumnBudgetBytes caps the materialised columns held at once, spilling
	// the least recently used past it. Zero, the default, keeps everything
	// resident — see sheet.Config.
	ColumnBudgetBytes int `json:"columnBudgetBytes,omitempty"`
}

type sheetOp struct {
	s *sheet.Sheet
}

// attachDelegate lets the planner offer the sheet a way to have its eligible
// aggregates computed by the source instead of scanned here. Called after the
// prefix is final, since that is what the aggregate query is derived from.
func (o *sheetOp) attachDelegate(classic ClassicExecutor, pushed *dsl.QueryDSL) {
	if classic == nil || pushed == nil {
		return
	}
	o.s.SetReduceDelegate(&reduceDelegate{classic: classic, pushed: pushed})
}

func (o *sheetOp) Name() string     { return "sheet" }
func (o *sheetOp) IsLiveSafe() bool { return true }

func (o *sheetOp) Apply(ctx context.Context, in []dsl.Row) ([]dsl.Row, error) {
	res, err := o.s.Apply(ctx, in)
	if err != nil {
		return nil, err
	}
	// A pipe stage hands on rows, so a sheet-wide scalar reaches the next
	// stage as a column holding the same value in every row. Written here
	// rather than in the engine so the engine can keep them distinct, which
	// the pushdown work will need.
	for name, val := range res.Scalars {
		for _, row := range res.Rows {
			row[name] = val
		}
	}
	return res.Rows, nil
}

func sheetFactory(raw json.RawMessage, octx *OpContext) (Operator, error) {
	var cfg SheetConfig
	if err := decodeConfig(raw, &cfg); err != nil {
		return nil, fmt.Errorf("sheet: decode config: %w", err)
	}
	if octx == nil || octx.ExprCompiler == nil {
		return nil, fmt.Errorf("sheet: requires %s in the OpContext", ReqExprCompiler)
	}
	s, err := sheet.Compile(sheet.Config{
		Formulas:          cfg.Formulas,
		OnError:           cfg.OnError,
		ColumnBudgetBytes: cfg.ColumnBudgetBytes,
	}, octx.ExprCompiler, sheet.WithRegistry(octx.SheetFuncs))
	if err != nil {
		return nil, err
	}
	return &sheetOp{s: s}, nil
}

func init() { Register("sheet", sheetFactory) }
