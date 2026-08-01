package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestTopPerGroup_partitioned(t *testing.T) {
	op, err := topPerGroupFactory(stageJSON(t, map[string]any{
		"op":          "topPerGroup",
		"n":           2,
		"by":          []map[string]any{{"field": "score", "dir": "desc"}},
		"partitionBy": []string{"category"},
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{
		{"category": "A", "score": 10.0},
		{"category": "A", "score": 30.0},
		{"category": "A", "score": 20.0},
		{"category": "B", "score": 100.0},
		{"category": "B", "score": 50.0},
	}
	out, _ := op.Apply(context.Background(), in)
	if len(out) != 4 {
		t.Fatalf("expected 4 rows (top 2 of A + top 2 of B), got %d", len(out))
	}
	// First two should be category A, score 30 then 20.
	if out[0]["score"] != 30.0 || out[1]["score"] != 20.0 {
		t.Fatalf("A bucket wrong: %+v %+v", out[0], out[1])
	}
}

func TestTopPerGroup_globalTopN(t *testing.T) {
	op, _ := topPerGroupFactory(stageJSON(t, map[string]any{
		"op": "topPerGroup",
		"n":  3,
		"by": []map[string]any{{"field": "v", "dir": "desc"}},
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"v": 1.0}, {"v": 5.0}, {"v": 3.0}, {"v": 4.0}, {"v": 2.0},
	})
	if len(out) != 3 {
		t.Fatalf("rows: %d", len(out))
	}
	if out[0]["v"] != 5.0 || out[2]["v"] != 3.0 {
		t.Fatalf("top 3 wrong: %+v", out)
	}
}

func TestTopPerGroup_invalidN(t *testing.T) {
	_, err := topPerGroupFactory(stageJSON(t, map[string]any{
		"op": "topPerGroup", "n": 0, "by": []map[string]any{{"field": "x"}},
	}), nil)
	if err == nil {
		t.Fatalf("expected error for n=0")
	}
}
