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

func TestEvalSimpleCondition_caseInsensitiveOps(t *testing.T) {
	row := dsl.Row{"name": "Acme Corp", "count": 42}
	cases := []struct {
		op, field string
		value     any
		want      bool
	}{
		{"icontains", "name", "acme", true},
		{"icontains", "name", "ME CO", true},
		{"icontains", "name", "globex", false},
		{"istarts_with", "name", "ACME", true},
		{"istarts_with", "name", "corp", false},
		{"iends_with", "name", "CORP", true},
		{"iends_with", "name", "acme", false},
		// The value is a literal: SQL wildcards in it match only themselves.
		{"icontains", "name", "%", false},
		{"icontains", "name", "a_me", false},
		// Non-string cells compare on their printed form.
		{"icontains", "count", "4", true},
		// A missing or nil cell never matches.
		{"icontains", "missing", "a", false},
	}
	for _, tc := range cases {
		w := &dsl.WhereClause{Field: tc.field, Op: tc.op, Value: tc.value}
		if got := evalSimpleCondition(w, row); got != tc.want {
			t.Errorf("op=%q field=%q value=%v: got %v, want %v", tc.op, tc.field, tc.value, got, tc.want)
		}
	}
}

func TestEvalSimpleCondition_icontainsLiteralWildcards(t *testing.T) {
	row := dsl.Row{"label": "10%_off"}
	w := &dsl.WhereClause{Field: "label", Op: "icontains", Value: "0%_O"}
	if !evalSimpleCondition(w, row) {
		t.Errorf("expected literal %%_ to match itself case-insensitively")
	}
}
