package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestHistogram_bucketsValues(t *testing.T) {
	op, err := histogramFactory(stageJSON(t, map[string]any{
		"op":    "histogram",
		"field": "v",
		"bins":  4,
		"min":   0.0,
		"max":   100.0,
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{
		{"v": 5.0},
		{"v": 25.0},
		{"v": 50.0},
		{"v": 75.0},
		{"v": 99.0},
	}
	out, _ := op.Apply(context.Background(), in)
	if len(out) != 4 {
		t.Fatalf("expected 4 bins, got %d", len(out))
	}
	// Bin 0: [0,25). Bin 1: [25,50). Bin 2: [50,75). Bin 3: [75,100].
	counts := []int{out[0]["count"].(int), out[1]["count"].(int), out[2]["count"].(int), out[3]["count"].(int)}
	if counts[0] != 1 || counts[1] != 1 || counts[2] != 1 || counts[3] != 2 {
		t.Fatalf("bin counts wrong: %+v", counts)
	}
}

func TestHistogram_derivesMinMax(t *testing.T) {
	op, _ := histogramFactory(stageJSON(t, map[string]any{
		"op":    "histogram",
		"field": "v",
		"bins":  2,
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"v": 0.0}, {"v": 10.0},
	})
	if len(out) != 2 {
		t.Fatalf("rows: %d", len(out))
	}
}

func TestHistogram_emptyInput(t *testing.T) {
	op, _ := histogramFactory(stageJSON(t, map[string]any{
		"op":    "histogram",
		"field": "v",
		"bins":  2,
	}), nil)
	out, err := op.Apply(context.Background(), nil)
	if err != nil {
		t.Fatalf("empty input should not error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("empty input should give zero rows: %+v", out)
	}
}
