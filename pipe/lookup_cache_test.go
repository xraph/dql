package pipe

import (
	"context"
	"testing"
	"time"

	"github.com/xraph/dql/dsl"
)

// countingClassic counts how many right-side fetches happen.
type countingClassic struct {
	calls  int
	result *dsl.QueryResult
}

func (c *countingClassic) Execute(_ context.Context, _ *dsl.QueryDSL, _, _ string) (*dsl.QueryResult, error) {
	c.calls++
	return c.result, nil
}

func TestLookup_cache_servesFromCacheWithinTTL(t *testing.T) {
	classic := &countingClassic{result: dsl.NewQueryResult([]dsl.Row{{"id": "a", "name": "Alpha"}})}
	op, err := lookupFactory(stageJSON(t, map[string]any{
		"op":         "lookup",
		"dataset":    "sites",
		"on":         map[string]any{"left": "site", "right": "id"},
		"cacheTtlMs": 10000, // 10s
	}), &OpContext{Classic: classic})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	ctx := withScope(context.Background(), "ws1", "")
	for i := 0; i < 5; i++ {
		_, err := op.Apply(ctx, []dsl.Row{{"site": "a"}})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	if classic.calls != 1 {
		t.Fatalf("with cache, expected 1 right-side fetch, got %d", classic.calls)
	}
}

func TestLookup_cache_disabled_byDefault(t *testing.T) {
	classic := &countingClassic{result: dsl.NewQueryResult([]dsl.Row{{"id": "a"}})}
	op, err := lookupFactory(stageJSON(t, map[string]any{
		"op":      "lookup",
		"dataset": "sites",
		"on":      map[string]any{"left": "site", "right": "id"},
	}), &OpContext{Classic: classic})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	ctx := withScope(context.Background(), "ws1", "")
	for i := 0; i < 3; i++ {
		_, _ = op.Apply(ctx, []dsl.Row{{"site": "a"}})
	}
	if classic.calls != 3 {
		t.Fatalf("without cache, expected 3 fetches (one per Apply), got %d", classic.calls)
	}
}

func TestLookup_cache_expiresAfterTTL(t *testing.T) {
	classic := &countingClassic{result: dsl.NewQueryResult([]dsl.Row{{"id": "a"}})}
	op, err := lookupFactory(stageJSON(t, map[string]any{
		"op":         "lookup",
		"dataset":    "sites",
		"on":         map[string]any{"left": "site", "right": "id"},
		"cacheTtlMs": 1, // 1ms — expires almost immediately
	}), &OpContext{Classic: classic})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	ctx := withScope(context.Background(), "ws1", "")
	_, _ = op.Apply(ctx, []dsl.Row{{"site": "a"}})
	time.Sleep(5 * time.Millisecond) // wait past TTL
	_, _ = op.Apply(ctx, []dsl.Row{{"site": "a"}})
	if classic.calls != 2 {
		t.Fatalf("expected 2 fetches after TTL expiry, got %d", classic.calls)
	}
}
