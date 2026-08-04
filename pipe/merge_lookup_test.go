package pipe

import (
	"reflect"
	"testing"

	"github.com/xraph/dql/dsl"
)

// mergeLookup is shared by lookup, crossJoin, and asofJoin — the three most
// expensive operators in the suite — so its exact semantics are pinned here
// before any optimisation. Every case below characterises current behaviour.

func TestMergeLookup_noSelectNoAs_mergesEveryRightColumn(t *testing.T) {
	left := dsl.Row{"id": 1, "name": "a"}
	right := dsl.Row{"score": 9, "tag": "x"}
	got := mergeLookup(left, right, LookupConfig{})
	want := dsl.Row{"id": 1, "name": "a", "score": 9, "tag": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeLookup_asPrefixesEveryRightColumn(t *testing.T) {
	left := dsl.Row{"id": 1}
	right := dsl.Row{"score": 9, "tag": "x"}
	got := mergeLookup(left, right, LookupConfig{As: "r"})
	want := dsl.Row{"id": 1, "r_score": 9, "r_tag": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeLookup_selectLimitsColumns(t *testing.T) {
	left := dsl.Row{"id": 1}
	right := dsl.Row{"score": 9, "tag": "x", "extra": true}
	got := mergeLookup(left, right, LookupConfig{Select: []string{"score"}})
	want := dsl.Row{"id": 1, "score": 9}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeLookup_selectWithAs(t *testing.T) {
	left := dsl.Row{"id": 1}
	right := dsl.Row{"score": 9, "tag": "x"}
	got := mergeLookup(left, right, LookupConfig{As: "r", Select: []string{"tag"}})
	want := dsl.Row{"id": 1, "r_tag": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Selecting a column the right row does not have writes an explicit nil rather
// than omitting the key. Callers can distinguish "no match" from "matched but
// absent", so this is load-bearing.
func TestMergeLookup_selectMissingColumnWritesNil(t *testing.T) {
	left := dsl.Row{"id": 1}
	right := dsl.Row{"score": 9}
	got := mergeLookup(left, right, LookupConfig{Select: []string{"absent"}})
	v, present := got["absent"]
	if !present {
		t.Fatal("selected column missing from output; it should be present and nil")
	}
	if v != nil {
		t.Fatalf("want nil for absent column, got %v", v)
	}
}

// Right side is written after left, so on a name collision the right value wins.
func TestMergeLookup_rightWinsOnKeyCollision(t *testing.T) {
	left := dsl.Row{"id": 1, "score": "left"}
	right := dsl.Row{"score": "right"}
	got := mergeLookup(left, right, LookupConfig{})
	if got["score"] != "right" {
		t.Fatalf("right side should win a collision, got %v", got["score"])
	}
}

// The result must be a new map: mutating it must not disturb the input rows,
// which the operators reuse.
func TestMergeLookup_doesNotMutateInputs(t *testing.T) {
	left := dsl.Row{"id": 1}
	right := dsl.Row{"score": 9}
	got := mergeLookup(left, right, LookupConfig{As: "r"})
	got["injected"] = true
	if _, leaked := left["injected"]; leaked {
		t.Fatal("merge leaked into the left row")
	}
	if _, leaked := right["injected"]; leaked {
		t.Fatal("merge leaked into the right row")
	}
	if len(left) != 1 || len(right) != 1 {
		t.Fatalf("inputs changed size: left=%v right=%v", left, right)
	}
}

func TestMergeLookup_emptyRight(t *testing.T) {
	left := dsl.Row{"id": 1}
	got := mergeLookup(left, dsl.Row{}, LookupConfig{As: "r"})
	want := dsl.Row{"id": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
