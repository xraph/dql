package pipe

import (
	"context"
	"testing"
	"time"

	"github.com/xraph/dql/dsl"
)

func TestTimeBucket_5m(t *testing.T) {
	op, err := timeBucketFactory(stageJSON(t, map[string]any{
		"op":       "timeBucket",
		"field":    "ts",
		"interval": "5m",
		"as":       "bucket",
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	out, err := op.Apply(context.Background(), []dsl.Row{
		{"ts": "2026-04-25T10:02:30Z", "v": 1},
		{"ts": "2026-04-25T10:07:00Z", "v": 2},
		{"ts": "2026-04-25T10:12:00Z", "v": 3},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out[0]["bucket"] != "2026-04-25T10:00:00Z" {
		t.Fatalf("row0 bucket: %+v", out[0])
	}
	if out[1]["bucket"] != "2026-04-25T10:05:00Z" {
		t.Fatalf("row1 bucket: %+v", out[1])
	}
	if out[2]["bucket"] != "2026-04-25T10:10:00Z" {
		t.Fatalf("row2 bucket: %+v", out[2])
	}
}

func TestTimeBucket_dayInterval(t *testing.T) {
	op, _ := timeBucketFactory(stageJSON(t, map[string]any{
		"op":       "timeBucket",
		"field":    "ts",
		"interval": "1d",
		"as":       "day",
	}), nil)
	out, _ := op.Apply(context.Background(), []dsl.Row{{"ts": "2026-04-25T15:30:00Z"}})
	if out[0]["day"] != "2026-04-25T00:00:00Z" {
		t.Fatalf("day bucket: %+v", out[0])
	}
}

func TestTimeBucket_invalidInterval_factoryErrors(t *testing.T) {
	_, err := timeBucketFactory(stageJSON(t, map[string]any{
		"op":       "timeBucket",
		"field":    "ts",
		"interval": "nonsense",
		"as":       "b",
	}), nil)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestGapfill_fillsMissingBuckets(t *testing.T) {
	op, err := gapfillFactory(stageJSON(t, map[string]any{
		"op":       "gapfill",
		"field":    "ts",
		"interval": "1m",
		"method":   "zero",
	}), nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{
		{"ts": "2026-04-25T10:00:00Z", "v": 1.0},
		// 10:01 is missing
		{"ts": "2026-04-25T10:02:00Z", "v": 2.0},
	}
	out, err := op.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(out))
	}
	if out[1]["v"] != 0 {
		t.Fatalf("missing bucket should have v=0 (zero-fill): %+v", out[1])
	}
}

func TestGapfill_partitioned(t *testing.T) {
	op, _ := gapfillFactory(stageJSON(t, map[string]any{
		"op":       "gapfill",
		"field":    "ts",
		"interval": "1m",
		"groupBy":  []string{"host"},
		"method":   "lastValue",
	}), nil)
	in := []dsl.Row{
		{"ts": "2026-04-25T10:00:00Z", "host": "a", "v": 10.0},
		{"ts": "2026-04-25T10:02:00Z", "host": "a", "v": 12.0},
		{"ts": "2026-04-25T10:00:00Z", "host": "b", "v": 5.0},
	}
	out, _ := op.Apply(context.Background(), in)
	// 3 input rows + 1 synthetic for host=a at 10:01.
	if len(out) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(out))
	}
}

func TestAsofJoin_backwardMatch(t *testing.T) {
	right := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"sym": "A", "ts": "2026-04-25T10:00:00Z", "px": 100.0},
		{"sym": "A", "ts": "2026-04-25T10:05:00Z", "px": 110.0},
	})}
	op, err := asofJoinFactory(stageJSON(t, map[string]any{
		"op":        "asofJoin",
		"dataset":   "prices",
		"leftTime":  "ts",
		"rightTime": "ts",
		"leftKey":   "sym",
		"rightKey":  "sym",
		"select":    []string{"px"},
	}), &OpContext{Classic: right})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	ctx := withScope(context.Background(), "ws1", "")
	out, err := op.Apply(ctx, []dsl.Row{
		{"sym": "A", "ts": "2026-04-25T10:03:00Z", "qty": 10},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out[0]["px"] != 100.0 {
		t.Fatalf("expected backward match px=100, got %+v", out[0])
	}
}

func TestAsofJoin_toleranceExcludes(t *testing.T) {
	right := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"ts": "2026-04-25T10:00:00Z", "px": 100.0},
	})}
	op, _ := asofJoinFactory(stageJSON(t, map[string]any{
		"op":        "asofJoin",
		"dataset":   "prices",
		"leftTime":  "ts",
		"rightTime": "ts",
		"tolerance": "30s",
		"select":    []string{"px"},
	}), &OpContext{Classic: right})
	ctx := withScope(context.Background(), "ws1", "")
	out, _ := op.Apply(ctx, []dsl.Row{
		{"ts": "2026-04-25T10:05:00Z"},
	})
	if _, has := out[0]["px"]; has {
		t.Fatalf("tolerance should have excluded the match: %+v", out[0])
	}
}

func TestAsofJoin_nearestDirection(t *testing.T) {
	right := &stubClassic{result: dsl.NewQueryResult([]dsl.Row{
		{"ts": "2026-04-25T10:00:00Z", "px": 100.0},
		{"ts": "2026-04-25T10:10:00Z", "px": 200.0},
	})}
	op, _ := asofJoinFactory(stageJSON(t, map[string]any{
		"op":        "asofJoin",
		"dataset":   "prices",
		"leftTime":  "ts",
		"rightTime": "ts",
		"direction": "nearest",
		"select":    []string{"px"},
	}), &OpContext{Classic: right})
	ctx := withScope(context.Background(), "ws1", "")
	out, _ := op.Apply(ctx, []dsl.Row{
		{"ts": "2026-04-25T10:07:00Z"}, // closer to 10:10 (3 min) than 10:00 (7 min)
	})
	if out[0]["px"] != 200.0 {
		t.Fatalf("nearest match wrong: %+v", out[0])
	}
}

func TestParseInterval_humanForms(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"5m", 5 * time.Minute},
		{"1h", time.Hour},
		{"1d", 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
	}
	for _, c := range cases {
		got, err := parseInterval(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
}
