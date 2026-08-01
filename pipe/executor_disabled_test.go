package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestNewExecutor_preservesPreSetClassic(t *testing.T) {
	primary := &stubClassic{}
	preset := &stubClassic{}
	octx := &OpContext{Classic: preset}

	x := NewExecutor(primary, octx, ExecutorConfig{})
	if x.OpContext().Classic != preset {
		t.Fatalf("NewExecutor must not overwrite a pre-set Classic; expected the pre-set instance")
	}
}

func TestNewExecutor_setsClassic_whenContextIsBare(t *testing.T) {
	primary := &stubClassic{}
	x := NewExecutor(primary, &OpContext{}, ExecutorConfig{})
	if x.OpContext().Classic != primary {
		t.Fatalf("NewExecutor should fall back to the supplied classic when OpContext.Classic is nil")
	}
}

func TestExecutor_inMemoryTail_preventsDefaultLimitClip(t *testing.T) {
	// 50 rows from classic. We have 1 in-memory op (compute). The pipe should
	// NOT clip the classic prefix to a small default limit — it should pass
	// the full set through compute.
	rows := make([]dsl.Row, 50)
	for i := range rows {
		rows[i] = dsl.Row{"v": i}
	}
	classic := &stubClassic{result: dsl.NewQueryResult(rows)}
	eval := &mockEval{results: map[string]any{"v + 1": 99.0}}
	x := NewExecutor(classic, &OpContext{Eval: eval}, ExecutorConfig{MaxRows: 1000})

	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{mustStage(t, "compute", map[string]any{"as": "b", "expr": "v + 1"})},
	}
	res, err := x.Execute(context.Background(), q, "ws", "proj")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Rows) != 50 {
		t.Fatalf("expected 50 rows after compute, got %d (default-limit clipped?)", len(res.Rows))
	}
	if classic.last.Limit == nil {
		t.Fatalf("pushed prefix should carry an explicit Limit when in-memory ops follow")
	}
}
