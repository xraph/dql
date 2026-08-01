package pipe

import (
	"context"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestPivot_basicSum(t *testing.T) {
	op, err := pivotFactory(stageJSON(t, map[string]any{
		"op":         "pivot",
		"rowKeys":    []string{"host"},
		"columnKey":  "metric",
		"valueField": "value",
		"aggregate":  "sum",
		"fillValue":  0,
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{
		{"host": "h1", "metric": "cpu", "value": 50.0},
		{"host": "h1", "metric": "mem", "value": 2.0},
		{"host": "h2", "metric": "cpu", "value": 80.0},
	}
	out, _ := op.Apply(context.Background(), in)
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
	row0 := out[0]
	if row0["host"] != "h1" || row0["cpu"] != 50.0 || row0["mem"] != 2.0 {
		t.Fatalf("h1 row wrong: %+v", row0)
	}
	row1 := out[1]
	if row1["host"] != "h2" || row1["cpu"] != 80.0 {
		t.Fatalf("h2 row wrong: %+v", row1)
	}
	// fillValue applied: h2 has no mem (JSON marshals 0 as float64(0))
	if row1["mem"] != float64(0) {
		t.Fatalf("h2 mem should be filled with 0, got %v (%T)", row1["mem"], row1["mem"])
	}
}

func TestUnpivot_explicitValueCols(t *testing.T) {
	op, err := unpivotFactory(stageJSON(t, map[string]any{
		"op":        "unpivot",
		"idCols":    []string{"id"},
		"valueCols": []string{"a", "b"},
		"nameAs":    "metric",
		"valueAs":   "value",
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"id": 1, "a": 10, "b": 20},
		{"id": 2, "a": 30, "b": 40},
	})
	if len(out) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(out))
	}
	if out[0]["metric"] != "a" || out[0]["value"] != 10 {
		t.Fatalf("first row wrong: %+v", out[0])
	}
}

func TestUnnestObject_spreadsKeys(t *testing.T) {
	op, _ := unnestObjectFactory(stageJSON(t, map[string]any{
		"op":     "unnestObject",
		"field":  "meta",
		"prefix": "m_",
		"drop":   true,
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{
		{"id": 1, "meta": map[string]any{"region": "us", "tier": 2}},
	})
	if out[0]["m_region"] != "us" || out[0]["m_tier"] != 2 {
		t.Fatalf("unnest wrong: %+v", out[0])
	}
	if _, has := out[0]["meta"]; has {
		t.Fatalf("drop=true should remove source field: %+v", out[0])
	}
}

func TestNest_groupsRows(t *testing.T) {
	op, err := nestFactory(stageJSON(t, map[string]any{
		"op":   "nest",
		"by":   []string{"host"},
		"into": "events",
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{
		{"host": "h1", "ts": 1, "msg": "a"},
		{"host": "h2", "ts": 2, "msg": "b"},
		{"host": "h1", "ts": 3, "msg": "c"},
	}
	out, _ := op.Apply(context.Background(), in)
	if len(out) != 2 {
		t.Fatalf("expected 2 grouped rows, got %d", len(out))
	}
	// h1 group has 2 events.
	for _, row := range out {
		if row["host"] == "h1" {
			arr := row["events"].([]map[string]any)
			if len(arr) != 2 {
				t.Fatalf("h1 group should have 2 events, got %d", len(arr))
			}
		}
	}
}
