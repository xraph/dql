package pipe

import (
	"strings"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestClassify_purePipe_isSafe(t *testing.T) {
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "filter", map[string]any{"where": map[string]any{"field": "v", "op": ">", "value": 0}}),
			mustStage(t, "limit", map[string]any{"n": 10}),
		},
	}
	plan, err := PlanPipe(q, nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	c := Classify(plan)
	if !c.IsLiveSafe() {
		t.Fatalf("pure pipe should be live-safe: %+v", c)
	}
	if err := c.RejectError(); err != nil {
		t.Fatalf("RejectError should be nil for live-safe pipe, got %v", err)
	}
}

func TestClassify_callAppIsUnsafe(t *testing.T) {
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "callApp", map[string]any{"appId": "slack"}),
		},
	}
	plan, err := PlanPipe(q, &OpContext{AppCaller: &stubAppCaller{}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	c := Classify(plan)
	if c.IsLiveSafe() {
		t.Fatalf("callApp should be unsafe")
	}
	if c.UnsafeStages[0] != "callApp" {
		t.Fatalf("unsafe name wrong: %+v", c.UnsafeStages)
	}
	err = c.RejectError()
	if err == nil {
		t.Fatalf("expected reject error")
	}
	if !strings.Contains(err.Error(), "dryRun=true") {
		t.Fatalf("reject error should mention dryRun: %v", err)
	}
}

func TestClassify_pureCallFunctionIsSafe(t *testing.T) {
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "callFunction", map[string]any{"name": "math::abs", "pure": true}),
		},
	}
	plan, err := PlanPipe(q, &OpContext{Registry: &stubRegistry{returns: 1}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	c := Classify(plan)
	if !c.IsLiveSafe() {
		t.Fatalf("pure callFunction should be live-safe")
	}
}

func TestClassify_impureCallFunctionIsUnsafe(t *testing.T) {
	q := &dsl.QueryDSL{
		Mode: "pipe",
		From: dsl.FromClause{Dataset: "events"},
		Pipe: []dsl.PipeStage{
			mustStage(t, "callFunction", map[string]any{"name": "http::get"}),
		},
	}
	plan, err := PlanPipe(q, &OpContext{Registry: &stubRegistry{returns: 1}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	c := Classify(plan)
	if c.IsLiveSafe() {
		t.Fatalf("default callFunction (pure=false) should be unsafe")
	}
}
