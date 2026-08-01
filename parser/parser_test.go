package parser

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xraph/dql/dsl"
)

// --- Parser Tests ---

func TestParse_ValidJSON(t *testing.T) {
	raw := json.RawMessage(`{"from":"sensor_readings","where":{"field":"value","op":">","value":50}}`)
	q, errs := Parse(raw)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if q.From.Dataset != "sensor_readings" {
		t.Errorf("from: got %q, want %q", q.From.Dataset, "sensor_readings")
	}
	if q.Where == nil || q.Where.Field != "value" {
		t.Error("expected where clause with field 'value'")
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{invalid}`)
	_, errs := Parse(raw)
	if len(errs) == 0 {
		t.Fatal("expected parse errors for invalid JSON")
	}
	if errs[0].Field != "body" {
		t.Errorf("error field: got %q, want %q", errs[0].Field, "body")
	}
}

func TestParse_MissingFrom(t *testing.T) {
	raw := json.RawMessage(`{"where":{"field":"x","op":"==","value":1}}`)
	_, errs := Parse(raw)
	if len(errs) == 0 {
		t.Fatal("expected validation error for missing from")
	}
	found := false
	for _, e := range errs {
		if e.Field == "from" {
			found = true
		}
	}
	if !found {
		t.Error("expected error on 'from' field")
	}
}

func TestParse_FromString(t *testing.T) {
	raw := json.RawMessage(`{"from":"my_table"}`)
	q, errs := Parse(raw)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if q.From.Dataset != "my_table" {
		t.Errorf("from: got %q, want %q", q.From.Dataset, "my_table")
	}
}

func TestParse_FromObject(t *testing.T) {
	raw := json.RawMessage(`{"from":{"dataset":"my_table"}}`)
	q, errs := Parse(raw)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if q.From.Dataset != "my_table" {
		t.Errorf("from: got %q, want %q", q.From.Dataset, "my_table")
	}
}

func TestParse_SelectFieldString(t *testing.T) {
	raw := json.RawMessage(`{"from":"t","select":["col1","col2"]}`)
	q, errs := Parse(raw)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(q.Select) != 2 {
		t.Fatalf("select: got %d fields, want 2", len(q.Select))
	}
	if q.Select[0].Field != "col1" {
		t.Errorf("select[0]: got %q, want %q", q.Select[0].Field, "col1")
	}
}

func TestParse_ParameterResolution(t *testing.T) {
	raw := json.RawMessage(`{
		"from":"t",
		"where":{"field":"status","op":"==","value":"$status"},
		"parameters":{"$status":"active"}
	}`)
	q, errs := Parse(raw)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if q.Where.Value != "active" {
		t.Errorf("where value: got %v, want %q", q.Where.Value, "active")
	}
}

// --- Validate Tests ---

func TestValidate_ValidQuery(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
	}
	errs := Validate(q)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestValidate_WhereOps(t *testing.T) {
	tests := []struct {
		op      string
		wantErr bool
	}{
		{"==", false},
		{"!=", false},
		{">", false},
		{"<", false},
		{">=", false},
		{"<=", false},
		{"in", false},
		{"not_in", false},
		{"like", false},
		{"is_null", false},
		{"is_not_null", false},
		{"between", false},
		{"invalid_op", true},
	}

	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			q := &dsl.QueryDSL{
				From: dsl.FromClause{Dataset: "test"},
				Where: &dsl.WhereClause{
					Field: "col",
					Op:    tt.op,
					Value: 1,
				},
			}
			// Unary ops don't need value
			if tt.op == "is_null" || tt.op == "is_not_null" {
				q.Where.Value = nil
			}
			errs := Validate(q)
			hasErr := len(errs) > 0
			if hasErr != tt.wantErr {
				t.Errorf("op %q: hasErr=%v, wantErr=%v, errs=%v", tt.op, hasErr, tt.wantErr, errs)
			}
		})
	}
}

func TestValidate_WhereValueRequired(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Where: &dsl.WhereClause{
			Field: "col",
			Op:    "==",
			Value: nil,
		},
	}
	errs := Validate(q)
	if len(errs) == 0 {
		t.Fatal("expected error for missing value with == op")
	}
}

func TestValidate_WhereUnaryNoValue(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Where: &dsl.WhereClause{
			Field: "col",
			Op:    "is_null",
		},
	}
	errs := Validate(q)
	if len(errs) > 0 {
		t.Fatalf("is_null should not require value, got: %v", errs)
	}
}

func TestValidate_HavingRequiresGroupBy(t *testing.T) {
	q := &dsl.QueryDSL{
		From:   dsl.FromClause{Dataset: "test"},
		Having: &dsl.WhereClause{Field: "cnt", Op: ">", Value: 5},
	}
	errs := Validate(q)
	found := false
	for _, e := range errs {
		if e.Field == "having" {
			found = true
		}
	}
	if !found {
		t.Error("expected having validation error")
	}
}

func TestValidate_HavingWithAggregate(t *testing.T) {
	q := &dsl.QueryDSL{
		From:      dsl.FromClause{Dataset: "test"},
		Aggregate: []dsl.AggregateClause{{Fn: "COUNT", As: "cnt"}},
		Having:    &dsl.WhereClause{Field: "cnt", Op: ">", Value: 5},
	}
	errs := Validate(q)
	// Should not have "having" error specifically
	for _, e := range errs {
		if e.Field == "having" {
			t.Errorf("unexpected having error: %v", e)
		}
	}
}

func TestValidate_JoinValidation(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Join: []dsl.JoinClause{
			{Dataset: "", On: dsl.JoinOn{Left: "a", Right: "b"}, Type: "inner"},
		},
	}
	errs := Validate(q)
	if len(errs) == 0 {
		t.Fatal("expected error for missing join dataset")
	}
}

func TestValidate_JoinMissingOnColumns(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Join: []dsl.JoinClause{
			{Dataset: "other", On: dsl.JoinOn{Left: "", Right: ""}, Type: "inner"},
		},
	}
	errs := Validate(q)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Field, ".on") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for missing join on columns")
	}
}

func TestValidate_JoinInvalidType(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Join: []dsl.JoinClause{
			{Dataset: "other", On: dsl.JoinOn{Left: "a", Right: "b"}, Type: "weird"},
		},
	}
	errs := Validate(q)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "unknown join type") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for invalid join type")
	}
}

func TestValidate_AggregateValidation(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Aggregate: []dsl.AggregateClause{
			{Fn: "", Field: "val", As: "total"},
		},
	}
	errs := Validate(q)
	if len(errs) == 0 {
		t.Fatal("expected error for missing aggregate function")
	}
}

func TestValidate_AggregateUnknownFn(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Aggregate: []dsl.AggregateClause{
			{Fn: "BOGUS", Field: "val", As: "total"},
		},
	}
	errs := Validate(q)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "unknown aggregate") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for unknown aggregate function")
	}
}

func TestValidate_AggregateMissingAlias(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Aggregate: []dsl.AggregateClause{
			{Fn: "SUM", Field: "val", As: ""},
		},
	}
	errs := Validate(q)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Field, ".as") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for missing aggregate alias")
	}
}

func TestValidate_ComputedColumnValidation(t *testing.T) {
	q := &dsl.QueryDSL{
		From:     dsl.FromClause{Dataset: "test"},
		Computed: []dsl.ComputedColumn{{Name: "", Expr: ""}},
	}
	errs := Validate(q)
	if len(errs) < 2 {
		t.Errorf("expected errors for missing name and expr, got %d", len(errs))
	}
}

func TestValidate_OrderByValidation(t *testing.T) {
	q := &dsl.QueryDSL{
		From:    dsl.FromClause{Dataset: "test"},
		OrderBy: []dsl.OrderByClause{{Field: "", Dir: "up"}},
	}
	errs := Validate(q)
	if len(errs) < 2 {
		t.Errorf("expected errors for missing field and invalid dir, got %d", len(errs))
	}
}

func TestValidate_SelectFieldValidation(t *testing.T) {
	q := &dsl.QueryDSL{
		From:   dsl.FromClause{Dataset: "test"},
		Select: []dsl.SelectField{{Field: "", Expr: ""}},
	}
	errs := Validate(q)
	if len(errs) == 0 {
		t.Fatal("expected error for select field without field or expr")
	}
}

func TestValidate_NegativeLimit(t *testing.T) {
	neg := -1
	q := &dsl.QueryDSL{
		From:  dsl.FromClause{Dataset: "test"},
		Limit: &neg,
	}
	errs := Validate(q)
	found := false
	for _, e := range errs {
		if e.Field == "limit" {
			found = true
		}
	}
	if !found {
		t.Error("expected limit validation error")
	}
}

func TestValidate_NegativeOffset(t *testing.T) {
	neg := -1
	q := &dsl.QueryDSL{
		From:   dsl.FromClause{Dataset: "test"},
		Offset: &neg,
	}
	errs := Validate(q)
	found := false
	for _, e := range errs {
		if e.Field == "offset" {
			found = true
		}
	}
	if !found {
		t.Error("expected offset validation error")
	}
}

func TestValidate_CompoundWhere(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Where: &dsl.WhereClause{
			And: []dsl.WhereClause{
				{Field: "a", Op: "==", Value: 1},
				{Field: "b", Op: "bad_op", Value: 2},
			},
		},
	}
	errs := Validate(q)
	if len(errs) == 0 {
		t.Fatal("expected error for bad_op in compound where")
	}
}

func TestValidate_ExprAggregate(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Aggregate: []dsl.AggregateClause{
			{Fn: "EXPR", Expr: "", As: "custom"},
		},
	}
	errs := Validate(q)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "expr is required") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for EXPR aggregate without expr")
	}
}

// --- Normalization Tests ---

func TestNormalizeAggregateFns(t *testing.T) {
	q := &dsl.QueryDSL{
		Aggregate: []dsl.AggregateClause{
			{Fn: "sum", As: "total"},
			{Fn: "count", As: "cnt"},
		},
	}
	NormalizeAggregateFns(q)
	if q.Aggregate[0].Fn != "SUM" {
		t.Errorf("fn: got %q, want %q", q.Aggregate[0].Fn, "SUM")
	}
	if q.Aggregate[1].Fn != "COUNT" {
		t.Errorf("fn: got %q, want %q", q.Aggregate[1].Fn, "COUNT")
	}
}

func TestNormalizeOrderDir(t *testing.T) {
	q := &dsl.QueryDSL{
		OrderBy: []dsl.OrderByClause{
			{Field: "a", Dir: ""},
			{Field: "b", Dir: "DESC"},
		},
	}
	NormalizeOrderDir(q)
	if q.OrderBy[0].Dir != "asc" {
		t.Errorf("dir: got %q, want %q", q.OrderBy[0].Dir, "asc")
	}
	if q.OrderBy[1].Dir != "desc" {
		t.Errorf("dir: got %q, want %q", q.OrderBy[1].Dir, "desc")
	}
}

// --- ResolveParameters Tests ---

func TestResolveParameters_NoParams(t *testing.T) {
	q := &dsl.QueryDSL{
		From:  dsl.FromClause{Dataset: "test"},
		Where: &dsl.WhereClause{Field: "x", Op: "==", Value: "$val"},
	}
	ResolveParameters(q)
	// No parameters map → value should stay as-is
	if q.Where.Value != "$val" {
		t.Errorf("value should remain %q, got %v", "$val", q.Where.Value)
	}
}

func TestResolveParameters_Substitution(t *testing.T) {
	q := &dsl.QueryDSL{
		From:       dsl.FromClause{Dataset: "test"},
		Where:      &dsl.WhereClause{Field: "x", Op: "==", Value: "$val"},
		Parameters: map[string]any{"$val": 42},
	}
	ResolveParameters(q)
	if q.Where.Value != 42 {
		t.Errorf("value: got %v, want 42", q.Where.Value)
	}
}

func TestResolveParameters_NestedCompound(t *testing.T) {
	q := &dsl.QueryDSL{
		From: dsl.FromClause{Dataset: "test"},
		Where: &dsl.WhereClause{
			And: []dsl.WhereClause{
				{Field: "a", Op: "==", Value: "$p1"},
				{Field: "b", Op: ">", Value: "$p2"},
			},
		},
		Parameters: map[string]any{"$p1": "hello", "$p2": 100},
	}
	ResolveParameters(q)
	if q.Where.And[0].Value != "hello" {
		t.Errorf("and[0].value: got %v, want %q", q.Where.And[0].Value, "hello")
	}
	if q.Where.And[1].Value != 100 {
		t.Errorf("and[1].value: got %v, want 100", q.Where.And[1].Value)
	}
}

// --- Pipe mode parser tests ---

func TestParse_PipeMode_ok(t *testing.T) {
	raw := json.RawMessage(`{
		"mode":"pipe",
		"from":"events",
		"pipe":[
			{"op":"filter","where":{"field":"level","op":"==","value":"ERROR"}},
			{"op":"limit","n":10}
		]
	}`)
	q, errs := Parse(raw)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !q.IsPipeMode() {
		t.Fatalf("not detected as pipe mode")
	}
	if len(q.Pipe) != 2 {
		t.Fatalf("pipe: got %d stages, want 2", len(q.Pipe))
	}
	if q.Pipe[0].Op != "filter" || q.Pipe[1].Op != "limit" {
		t.Fatalf("ops wrong: %+v", q.Pipe)
	}
}

func TestParse_PipeMode_requiresFrom(t *testing.T) {
	raw := json.RawMessage(`{"mode":"pipe","pipe":[{"op":"limit","n":1}]}`)
	_, errs := Parse(raw)
	if len(errs) == 0 {
		t.Fatal("expected error for missing from")
	}
	found := false
	for _, e := range errs {
		if e.Field == "from" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'from' error, got %+v", errs)
	}
}

func TestParse_PipeMode_emptyPipe(t *testing.T) {
	raw := json.RawMessage(`{"mode":"pipe","from":"events","pipe":[]}`)
	_, errs := Parse(raw)
	if len(errs) == 0 {
		t.Fatal("expected error for empty pipe")
	}
}

func TestParse_PipeMode_unknownOp(t *testing.T) {
	raw := json.RawMessage(`{"mode":"pipe","from":"events","pipe":[{"op":"frobnicate"}]}`)
	_, errs := Parse(raw)
	if len(errs) == 0 {
		t.Fatal("expected error for unknown op")
	}
}

func TestParse_PipeMode_ignoresClassicFieldValidation(t *testing.T) {
	// In pipe mode the classic top-level fields are ignored. A where clause
	// with an invalid operator would normally fail validation — here it
	// should be allowed through because it's not part of the pipe contract.
	raw := json.RawMessage(`{
		"mode":"pipe",
		"from":"events",
		"pipe":[{"op":"limit","n":10}],
		"where":{"field":"x","op":"===","value":1}
	}`)
	_, errs := Parse(raw)
	if len(errs) != 0 {
		t.Fatalf("pipe mode should skip classic where validation: %+v", errs)
	}
}

// --- Helper Tests ---

func TestFormatParseErrors_Single(t *testing.T) {
	errs := []ParseError{{Field: "from", Message: "required"}}
	got := FormatParseErrors(errs)
	if got != "from: required" {
		t.Errorf("got %q", got)
	}
}

func TestFormatParseErrors_Multiple(t *testing.T) {
	errs := []ParseError{
		{Field: "from", Message: "required"},
		{Message: "bad thing"},
	}
	got := FormatParseErrors(errs)
	if !strings.Contains(got, "2 errors") {
		t.Errorf("got %q, want '2 errors' prefix", got)
	}
}

func TestParseError_Error(t *testing.T) {
	e := ParseError{Field: "test", Message: "bad"}
	if e.Error() != "test: bad" {
		t.Errorf("got %q", e.Error())
	}

	e2 := ParseError{Message: "just message"}
	if e2.Error() != "just message" {
		t.Errorf("got %q", e2.Error())
	}
}

// --- Domain type tests ---

func TestFromClause_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
	}{
		{"string form", `"my_dataset"`, "my_dataset"},
		{"object form", `{"dataset":"my_dataset"}`, "my_dataset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f dsl.FromClause
			if err := json.Unmarshal([]byte(tt.input), &f); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if f.Dataset != tt.wantName {
				t.Errorf("dataset: got %q, want %q", f.Dataset, tt.wantName)
			}
		})
	}
}

func TestSelectField_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantField string
		wantAs    string
	}{
		{"string form", `"col1"`, "col1", ""},
		{"object form", `{"field":"col1","as":"c1"}`, "col1", "c1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s dsl.SelectField
			if err := json.Unmarshal([]byte(tt.input), &s); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if s.Field != tt.wantField {
				t.Errorf("field: got %q, want %q", s.Field, tt.wantField)
			}
			if s.As != tt.wantAs {
				t.Errorf("as: got %q, want %q", s.As, tt.wantAs)
			}
		})
	}
}

func TestSelectField_OutputName(t *testing.T) {
	tests := []struct {
		name string
		sf   dsl.SelectField
		want string
	}{
		{"alias", dsl.SelectField{Field: "x", As: "y"}, "y"},
		{"field only", dsl.SelectField{Field: "x"}, "x"},
		{"expr only", dsl.SelectField{Expr: "a + b"}, "a + b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sf.OutputName(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWhereClause_Methods(t *testing.T) {
	simple := &dsl.WhereClause{Field: "x", Op: "==", Value: 1}
	if !simple.IsSimple() {
		t.Error("expected IsSimple")
	}

	expr := &dsl.WhereClause{Expr: "x > 1"}
	if !expr.IsExpr() {
		t.Error("expected IsExpr")
	}

	compound := &dsl.WhereClause{And: []dsl.WhereClause{{Field: "x", Op: "==", Value: 1}}}
	if !compound.IsCompound() {
		t.Error("expected IsCompound")
	}
}

func TestAggregateClause_IsPushable(t *testing.T) {
	tests := []struct {
		fn   string
		expr string
		want bool
	}{
		{"COUNT", "", true},
		{"SUM", "", true},
		{"AVG", "", true},
		{"MIN", "", true},
		{"MAX", "", true},
		{"STDEV", "", false},
		{"COUNT", "x + 1", false},
	}
	for _, tt := range tests {
		t.Run(tt.fn, func(t *testing.T) {
			a := dsl.AggregateClause{Fn: tt.fn, Expr: tt.expr}
			if got := a.IsPushable(); got != tt.want {
				t.Errorf("IsPushable: got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestColumnMeta_IsRaw(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"", true},
		{"raw", true},
		{"formula", false},
		{"virtual", false},
		{"query", false},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			c := dsl.ColumnMeta{Source: tt.source}
			if got := c.IsRaw(); got != tt.want {
				t.Errorf("IsRaw: got %v, want %v", got, tt.want)
			}
		})
	}
}
