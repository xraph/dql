package pipe

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/dql/dsl"
)

type stubAppCaller struct {
	response    map[string]any
	err         error
	lastApp     string
	lastMethod  string
	lastPayload map[string]any
	calls       int
}

func (s *stubAppCaller) CallApp(_ context.Context, appID, method string, payload map[string]any) (map[string]any, error) {
	s.lastApp = appID
	s.lastMethod = method
	s.lastPayload = payload
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.response, nil
}

func TestCallApp_batch_sendsPayload(t *testing.T) {
	app := &stubAppCaller{response: map[string]any{"rows": []any{
		map[string]any{"id": 1, "enriched": true},
	}}}
	op, err := callAppFactory(stageJSON(t, map[string]any{
		"op":     "callApp",
		"appId":  "slack",
		"method": "transform",
	}), &OpContext{AppCaller: app})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	in := []dsl.Row{{"id": 1}, {"id": 2}}
	out, err := op.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if app.calls != 1 {
		t.Fatalf("batch should make 1 call, got %d", app.calls)
	}
	if app.lastPayload["capability"] != "pipe_query" {
		t.Fatalf("default capability not set: %+v", app.lastPayload)
	}
	rowsInPayload, ok := app.lastPayload["rows"].([]dsl.Row)
	if !ok || len(rowsInPayload) != 2 {
		t.Fatalf("rows in payload wrong: %+v", app.lastPayload["rows"])
	}
	if len(out) != 1 || out[0]["enriched"] != true {
		t.Fatalf("app response not honoured: %+v", out)
	}
}

func TestCallApp_perRow_callsOncePerRow(t *testing.T) {
	falseP := false
	app := &stubAppCaller{response: map[string]any{"rows": []any{
		map[string]any{"ok": true},
	}}}
	raw := stageJSON(t, map[string]any{
		"op":    "callApp",
		"appId": "slack",
		"batch": falseP,
	})
	op, err := callAppFactory(raw, &OpContext{AppCaller: app})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	_, err = op.Apply(context.Background(), []dsl.Row{{"id": 1}, {"id": 2}, {"id": 3}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if app.calls != 3 {
		t.Fatalf("per-row should make 3 calls, got %d", app.calls)
	}
}

func TestCallApp_neverLiveSafe(t *testing.T) {
	op, err := callAppFactory(stageJSON(t, map[string]any{"op": "callApp", "appId": "x"}), &OpContext{AppCaller: &stubAppCaller{}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if op.IsLiveSafe() {
		t.Fatalf("callApp must never be live-safe")
	}
}

func TestCallApp_missingAppCaller_errorsAtBuild(t *testing.T) {
	_, err := callAppFactory(stageJSON(t, map[string]any{"op": "callApp", "appId": "x"}), &OpContext{})
	if err == nil {
		t.Fatalf("expected error when appCaller is nil")
	}
}

func TestCallApp_missingAppID_errorsAtBuild(t *testing.T) {
	_, err := callAppFactory(stageJSON(t, map[string]any{"op": "callApp"}), &OpContext{AppCaller: &stubAppCaller{}})
	if err == nil {
		t.Fatalf("expected error when appID is missing")
	}
}

func TestCallApp_callError_wraps(t *testing.T) {
	app := &stubAppCaller{err: errors.New("network fail")}
	op, _ := callAppFactory(stageJSON(t, map[string]any{"op": "callApp", "appId": "slack"}), &OpContext{AppCaller: app})
	_, err := op.Apply(context.Background(), []dsl.Row{{}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestCallApp_responseWithoutRows_returnsInput(t *testing.T) {
	app := &stubAppCaller{response: map[string]any{"summaries": map[string]any{"count": 3}}}
	op, _ := callAppFactory(stageJSON(t, map[string]any{"op": "callApp", "appId": "slack"}), &OpContext{AppCaller: app})
	in := []dsl.Row{{"id": 1}}
	out, err := op.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out) != 1 || out[0]["id"] != 1 {
		t.Fatalf("input not passed through: %+v", out)
	}
}
