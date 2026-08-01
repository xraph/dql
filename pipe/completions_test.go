package pipe

import (
	"strings"
	"testing"
)

func TestCompleteText_atStartSuggestsSource(t *testing.T) {
	items := CompleteText("", 0, CompletionContext{})
	if !labelExists(items, "source") {
		t.Fatalf("expected 'source' suggestion at start: %+v", items)
	}
}

func TestCompleteText_afterSourceSuggestsDatasets(t *testing.T) {
	items := CompleteText("source ", 7, CompletionContext{Datasets: []string{"events", "users"}})
	if !labelExists(items, "events") {
		t.Fatalf("expected dataset 'events' in suggestions: %+v", items)
	}
}

func TestCompleteText_afterPipeSuggestsStages(t *testing.T) {
	items := CompleteText("source events | ", 16, CompletionContext{})
	if !labelExists(items, "where") || !labelExists(items, "limit") {
		t.Fatalf("expected stage keywords (where, limit): %+v", items)
	}
}

func TestCompleteText_whereSuggestsColumns(t *testing.T) {
	items := CompleteText("source events | where ", 22, CompletionContext{
		Columns: map[string][]string{"events": {"level", "host", "ts"}},
	})
	if !labelExists(items, "level") {
		t.Fatalf("expected column 'level': %+v", items)
	}
}

func TestCompleteText_whereOperatorSlot(t *testing.T) {
	items := CompleteText("source events | where level ", 28, CompletionContext{
		Columns: map[string][]string{"events": {"level"}},
	})
	if !labelExists(items, "==") {
		t.Fatalf("expected operator '==': %+v", items)
	}
}

func TestCompleteText_callFunctionSuggestsFunctions(t *testing.T) {
	items := CompleteText("source events | callFunction ", 29, CompletionContext{
		Functions: []string{"math::abs", "geo::lookup"},
	})
	if !labelExists(items, "math::abs") {
		t.Fatalf("expected function 'math::abs': %+v", items)
	}
}

func TestCompleteText_callAppSuggestsApps(t *testing.T) {
	items := CompleteText("source events | callApp ", 24, CompletionContext{Apps: []string{"slack", "pagerduty"}})
	if !labelExists(items, "slack") {
		t.Fatalf("expected app 'slack': %+v", items)
	}
}

func TestCompleteText_aggregateInsideParensSuggestsColumns(t *testing.T) {
	items := CompleteText("source events | aggregate count(", 32, CompletionContext{
		Columns: map[string][]string{"events": {"id"}},
	})
	if !labelExists(items, "*") || !labelExists(items, "id") {
		t.Fatalf("expected '*' and column 'id' inside parens: %+v", items)
	}
}

func TestCompleteText_aggregateOutsideParensSuggestsFns(t *testing.T) {
	items := CompleteText("source events | aggregate ", 26, CompletionContext{})
	if !labelExists(items, "count") {
		t.Fatalf("expected 'count': %+v", items)
	}
}

func TestCompleteText_partialMatchFiltersByPrefix(t *testing.T) {
	items := CompleteText("source events | wh", 18, CompletionContext{})
	if !labelExists(items, "where") {
		t.Fatalf("expected 'where' for prefix 'wh': %+v", items)
	}
	for _, it := range items {
		if !strings.HasPrefix(strings.ToLower(it.Label), "wh") {
			t.Fatalf("non-matching item leaked through: %+v", it)
		}
	}
}

func TestCompleteText_pipeInsideStringDoesNotSplit(t *testing.T) {
	// The '|' inside the string literal must not be treated as a stage
	// boundary. We verify by placing the cursor *after* the closing
	// quote and starting a fresh stage — if the splitter were broken, we'd
	// believe segment 2 starts at the literal '|' inside "a|b" and would
	// produce nonsense. Here, the only top-level '|' is the explicit
	// pipe-separator, so the new segment is empty → stage suggestions.
	text := `source events | where name == "a|b"` + "\n  | "
	items := CompleteText(text, len(text), CompletionContext{})
	if !labelExists(items, "where") || !labelExists(items, "limit") {
		t.Fatalf("string-internal '|' broke segment detection: %+v", items)
	}
}

func labelExists(items []CompletionItem, want string) bool {
	for _, it := range items {
		if it.Label == want {
			return true
		}
	}
	return false
}
