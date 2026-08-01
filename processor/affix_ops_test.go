package processor

import (
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestEvalSimpleCondition_startsWith(t *testing.T) {
	row := dsl.Row{"path": "/team/alpha/projects/foo"}
	w := &dsl.WhereClause{Field: "path", Op: "starts_with", Value: "/team/alpha"}
	if !evalSimpleCondition(w, row) {
		t.Errorf("expected match for prefix")
	}
	w.Value = "/team/beta"
	if evalSimpleCondition(w, row) {
		t.Errorf("expected no match for non-prefix")
	}
}

func TestEvalSimpleCondition_endsWith(t *testing.T) {
	row := dsl.Row{"path": "/team/alpha/projects/foo"}
	w := &dsl.WhereClause{Field: "path", Op: "ends_with", Value: "/foo"}
	if !evalSimpleCondition(w, row) {
		t.Errorf("expected match for suffix")
	}
	w.Value = "/bar"
	if evalSimpleCondition(w, row) {
		t.Errorf("expected no match")
	}
}

func TestEvalSimpleCondition_contains(t *testing.T) {
	row := dsl.Row{"path": "/team/alpha/projects/foo"}
	w := &dsl.WhereClause{Field: "path", Op: "contains", Value: "alpha"}
	if !evalSimpleCondition(w, row) {
		t.Errorf("expected match for substring")
	}
	w.Value = "gamma"
	if evalSimpleCondition(w, row) {
		t.Errorf("expected no match for absent substring")
	}
}

func TestEvalSimpleCondition_caseInsensitive(t *testing.T) {
	row := dsl.Row{"path": "/Team/Alpha"}
	cases := []struct {
		op, value string
	}{
		{"starts_with", "/team"},
		{"ends_with", "alpha"},
		{"contains", "TEAM"},
	}
	for _, tc := range cases {
		w := &dsl.WhereClause{Field: "path", Op: tc.op, Value: tc.value}
		if !evalSimpleCondition(w, row) {
			t.Errorf("op=%q value=%q: expected case-insensitive match", tc.op, tc.value)
		}
	}
}
