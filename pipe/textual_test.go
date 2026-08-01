package pipe

import (
	"encoding/json"
	"strings"
	"testing"
)

// stageOp returns the op name of the n-th stage.
func stageOp(t *testing.T, q []byte, n int) string {
	t.Helper()
	var dsl struct {
		Pipe []struct {
			Op string `json:"op"`
		} `json:"pipe"`
	}
	if err := json.Unmarshal(q, &dsl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if n >= len(dsl.Pipe) {
		t.Fatalf("stage %d out of range (have %d)", n, len(dsl.Pipe))
	}
	return dsl.Pipe[n].Op
}

func textToJSON(t *testing.T, text string) []byte {
	t.Helper()
	q, err := ParseText(text)
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestParseText_minimalSourceLimit(t *testing.T) {
	out := textToJSON(t, `source events | limit 10`)
	if !strings.Contains(string(out), `"mode":"pipe"`) {
		t.Fatalf("missing mode=pipe: %s", out)
	}
	if stageOp(t, out, 0) != "limit" {
		t.Fatalf("first stage wrong: %s", out)
	}
}

func TestParseText_simpleWhere(t *testing.T) {
	out := textToJSON(t, `source events | where level == "ERROR"`)
	if stageOp(t, out, 0) != "filter" {
		t.Fatalf("expected filter: %s", out)
	}
	if !strings.Contains(string(out), `"field":"level"`) {
		t.Fatalf("field not parsed: %s", out)
	}
	if !strings.Contains(string(out), `"value":"ERROR"`) {
		t.Fatalf("value not parsed: %s", out)
	}
}

func TestParseText_exprWhere(t *testing.T) {
	out := textToJSON(t, "source events | where `level == \"ERROR\" && code > 500`")
	if !strings.Contains(string(out), `"expr":`) {
		t.Fatalf("expr form not detected: %s", out)
	}
}

func TestParseText_compute_backtickExpr(t *testing.T) {
	out := textToJSON(t, "source events | compute host = `split(url, \"/\")[2]`")
	if stageOp(t, out, 0) != "compute" {
		t.Fatalf("expected compute: %s", out)
	}
	if !strings.Contains(string(out), `"expr":"split(url, \"/\")[2]"`) {
		t.Fatalf("expr not preserved: %s", out)
	}
}

func TestParseText_groupByAggregate_sortLimit(t *testing.T) {
	out := textToJSON(t, `source events
		| where level == "ERROR"
		| groupBy host
		| aggregate count(*) as n
		| sort n desc
		| limit 10`)
	stages := []string{}
	var dsl struct {
		Pipe []struct {
			Op string `json:"op"`
		} `json:"pipe"`
	}
	_ = json.Unmarshal(out, &dsl)
	for _, s := range dsl.Pipe {
		stages = append(stages, s.Op)
	}
	expected := []string{"filter", "groupBy", "aggregate", "sort", "limit"}
	for i, e := range expected {
		if i >= len(stages) || stages[i] != e {
			t.Fatalf("stage %d: got %v, want %v", i, stages, expected)
		}
	}
}

func TestParseText_callFunction_withArgsAndAs(t *testing.T) {
	out := textToJSON(t, `source events | callFunction geo::lookup args {"ip": "$ip"} as geo`)
	if stageOp(t, out, 0) != "callFunction" {
		t.Fatalf("expected callFunction: %s", out)
	}
	if !strings.Contains(string(out), `"name":"geo::lookup"`) {
		t.Fatalf("name not preserved: %s", out)
	}
	if !strings.Contains(string(out), `"as":"geo"`) {
		t.Fatalf("alias not preserved: %s", out)
	}
}

func TestParseText_distinct(t *testing.T) {
	out := textToJSON(t, `source events | distinct by host, level`)
	if stageOp(t, out, 0) != "distinct" {
		t.Fatalf("expected distinct: %s", out)
	}
	if !strings.Contains(string(out), `"by":["host","level"]`) {
		t.Fatalf("keys not preserved: %s", out)
	}
}

func TestParseText_renameDrop(t *testing.T) {
	out := textToJSON(t, `source events | rename id -> key | drop secret, password`)
	if stageOp(t, out, 0) != "rename" || stageOp(t, out, 1) != "drop" {
		t.Fatalf("stages wrong: %s", out)
	}
}

func TestParseText_noSource_errors(t *testing.T) {
	_, err := ParseText(`limit 10`)
	if err == nil {
		t.Fatalf("expected error for missing source")
	}
}

func TestParseText_unbalancedBrackets_errors(t *testing.T) {
	_, err := ParseText(`source events | callFunction f args {"a":`)
	if err == nil {
		t.Fatalf("expected error for unbalanced brackets")
	}
}

func TestParseText_unterminatedString_errors(t *testing.T) {
	_, err := ParseText(`source events | where name == "open`)
	if err == nil {
		t.Fatalf("expected error for unterminated string")
	}
}

func TestParseText_jsonRoundtrip_executesSameAsHandWritten(t *testing.T) {
	// Verify that the text-derived DSL can be re-parsed by the engine.
	q, err := ParseText(`source events | where x == 1 | limit 5`)
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if !q.IsPipeMode() {
		t.Fatalf("not pipe mode")
	}
	if q.From.Dataset != "events" {
		t.Fatalf("from wrong: %+v", q.From)
	}
	if len(q.Pipe) != 2 {
		t.Fatalf("pipe length: %d", len(q.Pipe))
	}
}

// stageConfig returns the n-th stage's config decoded as a generic map.
// Useful for asserting field-level shape of textual reshape ops.
func stageConfig(t *testing.T, q []byte, n int) map[string]any {
	t.Helper()
	var dsl struct {
		Pipe []json.RawMessage `json:"pipe"`
	}
	if err := json.Unmarshal(q, &dsl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if n >= len(dsl.Pipe) {
		t.Fatalf("stage %d out of range (have %d)", n, len(dsl.Pipe))
	}
	var cfg map[string]any
	if err := json.Unmarshal(dsl.Pipe[n], &cfg); err != nil {
		t.Fatalf("decode stage %d: %v", n, err)
	}
	return cfg
}

func TestParseText_pivot_sqlForm(t *testing.T) {
	out := textToJSON(t, `source orders | pivot product, sum(sales) for region, quarter`)
	if stageOp(t, out, 0) != "pivot" {
		t.Fatalf("expected pivot, got: %s", out)
	}
	cfg := stageConfig(t, out, 0)
	if cfg["columnKey"] != "product" {
		t.Fatalf("columnKey: %v", cfg["columnKey"])
	}
	if cfg["valueField"] != "sales" {
		t.Fatalf("valueField: %v", cfg["valueField"])
	}
	if cfg["aggregate"] != "sum" {
		t.Fatalf("aggregate: %v", cfg["aggregate"])
	}
	rks, _ := cfg["rowKeys"].([]any)
	if len(rks) != 2 || rks[0] != "region" || rks[1] != "quarter" {
		t.Fatalf("rowKeys: %v", cfg["rowKeys"])
	}
}

func TestParseText_pivot_implicitAggregate(t *testing.T) {
	// No agg fn — runtime defaults to "first".
	out := textToJSON(t, `source orders | pivot product, sales for region`)
	cfg := stageConfig(t, out, 0)
	if _, hasAgg := cfg["aggregate"]; hasAgg {
		t.Fatalf("aggregate should be omitted when not specified, got: %v", cfg["aggregate"])
	}
	if cfg["valueField"] != "sales" {
		t.Fatalf("valueField: %v", cfg["valueField"])
	}
}

func TestParseText_pivot_missingFor_errors(t *testing.T) {
	_, err := ParseText(`source orders | pivot product, sum(sales)`)
	if err == nil || !strings.Contains(err.Error(), "for") {
		t.Fatalf("expected `for` clause error, got: %v", err)
	}
}

func TestParseText_unpivot(t *testing.T) {
	out := textToJSON(t, `source sales | unpivot q1, q2, q3, q4 to (quarter, value)`)
	if stageOp(t, out, 0) != "unpivot" {
		t.Fatalf("expected unpivot, got: %s", out)
	}
	cfg := stageConfig(t, out, 0)
	vc, _ := cfg["valueCols"].([]any)
	if len(vc) != 4 || vc[0] != "q1" || vc[3] != "q4" {
		t.Fatalf("valueCols: %v", cfg["valueCols"])
	}
	if cfg["nameAs"] != "quarter" || cfg["valueAs"] != "value" {
		t.Fatalf("nameAs/valueAs: %v / %v", cfg["nameAs"], cfg["valueAs"])
	}
}

func TestParseText_unpivot_missingTo_errors(t *testing.T) {
	_, err := ParseText(`source sales | unpivot q1, q2`)
	if err == nil {
		t.Fatalf("expected error for missing `to`")
	}
}

func TestParseText_nest_basic(t *testing.T) {
	out := textToJSON(t, `source orders | nest by region, quarter into items`)
	if stageOp(t, out, 0) != "nest" {
		t.Fatalf("expected nest, got: %s", out)
	}
	cfg := stageConfig(t, out, 0)
	by, _ := cfg["by"].([]any)
	if len(by) != 2 || by[0] != "region" || by[1] != "quarter" {
		t.Fatalf("by: %v", cfg["by"])
	}
	if cfg["into"] != "items" {
		t.Fatalf("into: %v", cfg["into"])
	}
	if _, hasInclude := cfg["include"]; hasInclude {
		t.Fatalf("include should be omitted when not specified")
	}
}

func TestParseText_nest_withInclude(t *testing.T) {
	out := textToJSON(t, `source orders | nest by region into items include name, qty`)
	cfg := stageConfig(t, out, 0)
	inc, _ := cfg["include"].([]any)
	if len(inc) != 2 || inc[0] != "name" || inc[1] != "qty" {
		t.Fatalf("include: %v", cfg["include"])
	}
}

func TestParseText_unnestObject_variants(t *testing.T) {
	cases := []struct {
		text   string
		field  string
		prefix string
		drop   bool
	}{
		{`source events | unnestObject metadata`, "metadata", "", false},
		{`source events | unnestObject metadata drop`, "metadata", "", true},
		{`source events | unnestObject metadata prefix m_`, "metadata", "m_", false},
		{`source events | unnestObject metadata prefix m_ drop`, "metadata", "m_", true},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			out := textToJSON(t, tc.text)
			if stageOp(t, out, 0) != "unnestObject" {
				t.Fatalf("op: %s", out)
			}
			cfg := stageConfig(t, out, 0)
			if cfg["field"] != tc.field {
				t.Fatalf("field: %v", cfg["field"])
			}
			gotPrefix, _ := cfg["prefix"].(string)
			if gotPrefix != tc.prefix {
				t.Fatalf("prefix: got %q want %q", gotPrefix, tc.prefix)
			}
			gotDrop, _ := cfg["drop"].(bool)
			if gotDrop != tc.drop {
				t.Fatalf("drop: got %v want %v", gotDrop, tc.drop)
			}
		})
	}
}

// --- Lenient parsing (skeleton stages) ---

// TestParseTextLenient_emptyNest_diagnoses verifies that `| nest` with no
// arguments produces a placeholder stage and a diagnostic that names the
// missing required fields. This is the UI-editor flow: the user dropped a
// nest chip onto the canvas before filling the form.
func TestParseTextLenient_emptyNest_diagnoses(t *testing.T) {
	res, err := ParseTextLenient(`source events | nest`)
	if err != nil {
		t.Fatalf("ParseTextLenient: %v", err)
	}
	if res.Query == nil || len(res.Query.Pipe) != 1 {
		t.Fatalf("expected 1 placeholder stage, got %+v", res.Query)
	}
	if res.Query.Pipe[0].Op != "nest" {
		t.Fatalf("placeholder op: got %q want %q", res.Query.Pipe[0].Op, "nest")
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.StageIndex != 0 || d.Op != "nest" {
		t.Fatalf("diagnostic shape: %+v", d)
	}
	missing := strings.Join(d.Missing, ",")
	if !strings.Contains(missing, "by") || !strings.Contains(missing, "into") {
		t.Fatalf("missing fields: got %v want both 'by' and 'into'", d.Missing)
	}
}

// TestParseTextLenient_emptyPivot_diagnoses verifies pivot's required
// fields surface in the diagnostic.
func TestParseTextLenient_emptyPivot_diagnoses(t *testing.T) {
	res, err := ParseTextLenient(`source events | pivot`)
	if err != nil {
		t.Fatalf("ParseTextLenient: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(res.Diagnostics))
	}
	missing := strings.Join(res.Diagnostics[0].Missing, ",")
	if !strings.Contains(missing, "columnKey") || !strings.Contains(missing, "valueField") {
		t.Fatalf("missing fields: got %v want columnKey + valueField", res.Diagnostics[0].Missing)
	}
}

// TestParseTextLenient_mixedValidInvalid asserts valid stages parse fully
// while invalid ones produce diagnostics — the parser doesn't bail on the
// first failure.
func TestParseTextLenient_mixedValidInvalid(t *testing.T) {
	res, err := ParseTextLenient(`source events | where x == 1 | nest | limit 10`)
	if err != nil {
		t.Fatalf("ParseTextLenient: %v", err)
	}
	if len(res.Query.Pipe) != 3 {
		t.Fatalf("expected 3 stages (filter + nest placeholder + limit), got %d: %+v",
			len(res.Query.Pipe), res.Query.Pipe)
	}
	ops := []string{
		res.Query.Pipe[0].Op,
		res.Query.Pipe[1].Op,
		res.Query.Pipe[2].Op,
	}
	want := []string{"filter", "nest", "limit"}
	for i, o := range ops {
		if o != want[i] {
			t.Fatalf("stage %d op: got %q want %q (full: %v)", i, o, want[i], ops)
		}
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].StageIndex != 1 {
		t.Fatalf("want 1 diagnostic at index 1, got %+v", res.Diagnostics)
	}
}

// TestParseTextLenient_unknownOp_diagnoses verifies that an unknown op
// keyword (`| foo`) yields a diagnostic with no placeholder stage emitted
// and no `Missing` field — we don't know what fields it would need.
func TestParseTextLenient_unknownOp_diagnoses(t *testing.T) {
	res, err := ParseTextLenient(`source events | foo`)
	if err != nil {
		t.Fatalf("ParseTextLenient: %v", err)
	}
	if len(res.Query.Pipe) != 0 {
		t.Fatalf("unknown op should not emit a placeholder stage, got %+v", res.Query.Pipe)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(res.Diagnostics))
	}
	d := res.Diagnostics[0]
	if len(d.Missing) != 0 {
		t.Fatalf("unknown op shouldn't list Missing fields, got %v", d.Missing)
	}
	if !strings.Contains(d.Message, "unknown stage") {
		t.Fatalf("expected 'unknown stage' message, got %q", d.Message)
	}
}

// TestParseTextLenient_strictUnchanged confirms the strict ParseText
// still errors on inputs that the lenient mode tolerates.
func TestParseTextLenient_strictUnchanged(t *testing.T) {
	if _, err := ParseText(`source events | nest`); err == nil {
		t.Fatalf("strict ParseText must still error on `| nest`")
	}
}

// TestParseTextLenient_sourceErrorStillFails: tokenizer/source errors
// can't be recovered per-stage and still bubble up as a hard error.
func TestParseTextLenient_sourceErrorStillFails(t *testing.T) {
	if _, err := ParseTextLenient(`source events | where name == "open`); err == nil {
		t.Fatalf("unterminated string should still hard-error in lenient mode")
	}
}
