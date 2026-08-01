package pipe

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/xraph/dql/dsl"
)

// ParseText converts a textual pipe-mode query into a JSON QueryDSL.
//
// Grammar (informal):
//
//	program  := 'source' IDENT [ '|' stage ]*
//	stage    := where | select | compute | sort | limit | skip
//	          | groupBy | aggregate | distinct | flatten | rename | drop
//	          | tap | callFunction | callApp
//
// Expressions use a backtick-quoted form (`...`) to delimit DTL code so the
// parser can stop scanning at the next top-level '|' without ambiguity.
//
// Example:
//
//	source events
//	  | where level == "ERROR"
//	  | compute host = `split(url, "/")[2]`
//	  | groupBy host
//	  | aggregate count(*) as n
//	  | sort n desc
//	  | limit 10
func ParseText(text string) (*dsl.QueryDSL, error) {
	segments, err := splitTopLevel(text)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("textual: empty input")
	}

	q := &dsl.QueryDSL{Mode: "pipe"}

	// First segment must be a `source <ident>`.
	head := strings.TrimSpace(segments[0])
	src, err := parseSource(head)
	if err != nil {
		return nil, err
	}
	q.From = dsl.FromClause{Dataset: src}

	for i := 1; i < len(segments); i++ {
		stage, err := parseStage(strings.TrimSpace(segments[i]))
		if err != nil {
			return nil, fmt.Errorf("stage %d: %w", i, err)
		}
		q.Pipe = append(q.Pipe, stage)
	}
	return q, nil
}

// ParseDiagnostic describes a single parsing problem for one stage. The
// frontend uses these to render forms for incomplete stages without
// abandoning the whole parse.
type ParseDiagnostic struct {
	// StageIndex is the 0-based position in the pipe (matching q.Pipe[i]).
	// For an unknown-op diagnostic where no placeholder stage was emitted,
	// it still names the position the failed segment would have occupied.
	StageIndex int `json:"stage"`
	// Op is the op keyword recovered from the failed segment, when the
	// keyword itself was recognisable. Empty for unknown ops.
	Op string `json:"op,omitempty"`
	// Message is the human-readable error from the strict parser.
	Message string `json:"message"`
	// Missing lists required fields the parser couldn't fill, derived from
	// the op's catalog ConfigSchema. v1 reports every required field as
	// missing when the strict parser fails — partial-value extraction is
	// out of scope.
	Missing []string `json:"missing,omitempty"`
}

// ParseResult is what ParseTextLenient returns. Query contains every stage
// that parsed cleanly *plus* placeholder stages (op only, empty config) for
// the ones that failed shape validation. Diagnostics has one entry per
// failed stage so the caller can decide whether to render forms or 400 the
// request.
type ParseResult struct {
	Query       *dsl.QueryDSL     `json:"query"`
	Diagnostics []ParseDiagnostic `json:"diagnostics,omitempty"`
}

// ParseTextLenient is like ParseText but never errors on per-stage problems.
// Source-level errors (tokenizer failures: unterminated strings, unbalanced
// braces) still return an error — those make per-stage recovery impossible.
//
// Use this for UI editors that want to keep rendering forms while the user
// is still filling in skeleton stages. The strict ParseText stays the
// default for hand-written / scripted callers.
func ParseTextLenient(text string) (*ParseResult, error) {
	segments, err := splitTopLevel(text)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("textual: empty input")
	}

	q := &dsl.QueryDSL{Mode: "pipe"}
	src, err := parseSource(strings.TrimSpace(segments[0]))
	if err != nil {
		return nil, err
	}
	q.From = dsl.FromClause{Dataset: src}

	out := &ParseResult{Query: q}

	for i := 1; i < len(segments); i++ {
		seg := strings.TrimSpace(segments[i])
		stage, parseErr := parseStage(seg)
		if parseErr == nil {
			q.Pipe = append(q.Pipe, stage)
			continue
		}

		// Recover the op keyword if we can — the dispatch failed but
		// tokenisation may still have produced something usable.
		op := firstStageKeyword(seg)
		diag := ParseDiagnostic{
			StageIndex: i - 1, // 0-based within the Pipe array
			Op:         op,
			Message:    parseErr.Error(),
		}

		if op != "" && Known(op) {
			// Known op with bad/missing args: emit a placeholder stage so
			// downstream tooling sees the slot, and fill in the required
			// fields it's missing.
			diag.Missing = requiredFields(op)
			placeholder, _ := json.Marshal(map[string]any{"op": op})
			q.Pipe = append(q.Pipe, dsl.PipeStage{Op: op, Config: placeholder})
		}
		// Unknown op: no placeholder, just the diagnostic.

		out.Diagnostics = append(out.Diagnostics, diag)
	}

	return out, nil
}

// firstStageKeyword returns the op keyword (canonical form) for a textual
// segment, mapping the textual alias (e.g. "callfunction") onto the op
// name the rest of the system uses (e.g. "callFunction"). Returns "" when
// the segment is empty or starts with something unrecognisable.
func firstStageKeyword(seg string) string {
	tokens := tokenize(seg)
	if len(tokens) == 0 {
		return ""
	}
	first := strings.ToLower(tokens[0].text)
	switch first {
	case "where":
		return "filter"
	case "select":
		return "project"
	case "groupby":
		return "groupBy"
	case "callfunction":
		return "callFunction"
	case "callapp":
		return "callApp"
	case "unnestobject":
		return "unnestObject"
	}
	// For ops where the textual keyword matches the canonical name (most
	// of them) just return it. Fall through with the lower-cased keyword
	// so unknown keywords still reach the caller.
	return first
}

// requiredFields returns the names of fields the catalog marks as
// `required` for the given op. Returns nil when the op isn't in the
// catalog. Reads ConfigSchema["required"] directly — no schema parsing.
func requiredFields(op string) []string {
	for _, m := range Catalog() {
		if m.Name != op {
			continue
		}
		req, ok := m.ConfigSchema["required"].([]string)
		if !ok || len(req) == 0 {
			return nil
		}
		out := make([]string, len(req))
		copy(out, req)
		return out
	}
	return nil
}

// splitTopLevel splits on '|' while respecting "..." strings, `...` raw
// expressions, and balanced (), [], {}.
func splitTopLevel(s string) ([]string, error) {
	var out []string
	var b strings.Builder
	depthParen, depthBracket, depthBrace := 0, 0, 0
	inString := false
	inRaw := false
	escape := false

	for _, r := range s {
		if escape {
			b.WriteRune(r)
			escape = false
			continue
		}
		switch {
		case inString:
			b.WriteRune(r)
			switch r {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
		case inRaw:
			b.WriteRune(r)
			if r == '`' {
				inRaw = false
			}
		default:
			switch r {
			case '"':
				inString = true
				b.WriteRune(r)
			case '`':
				inRaw = true
				b.WriteRune(r)
			case '(':
				depthParen++
				b.WriteRune(r)
			case ')':
				depthParen--
				b.WriteRune(r)
			case '[':
				depthBracket++
				b.WriteRune(r)
			case ']':
				depthBracket--
				b.WriteRune(r)
			case '{':
				depthBrace++
				b.WriteRune(r)
			case '}':
				depthBrace--
				b.WriteRune(r)
			case '|':
				if depthParen == 0 && depthBracket == 0 && depthBrace == 0 {
					out = append(out, b.String())
					b.Reset()
				} else {
					b.WriteRune(r)
				}
			default:
				b.WriteRune(r)
			}
		}
	}
	if inString {
		return nil, fmt.Errorf("textual: unterminated string literal")
	}
	if inRaw {
		return nil, fmt.Errorf("textual: unterminated `expression`")
	}
	if depthParen != 0 || depthBracket != 0 || depthBrace != 0 {
		return nil, fmt.Errorf("textual: unbalanced brackets")
	}
	out = append(out, b.String())
	return out, nil
}

func parseSource(seg string) (string, error) {
	tokens := tokenize(seg)
	if len(tokens) < 2 || strings.ToLower(tokens[0].text) != "source" {
		return "", fmt.Errorf("textual: query must start with `source <dataset>`")
	}
	if tokens[1].kind != tokIdent && tokens[1].kind != tokString {
		return "", fmt.Errorf("textual: source expects a dataset name")
	}
	return tokens[1].value(), nil
}

func parseStage(seg string) (dsl.PipeStage, error) {
	tokens := tokenize(seg)
	if len(tokens) == 0 {
		return dsl.PipeStage{}, fmt.Errorf("empty stage")
	}
	first := strings.ToLower(tokens[0].text)
	type parser func([]token) (map[string]any, error)
	dispatch := map[string]struct {
		op string
		fn parser
	}{
		"where":        {"filter", parseWhere},
		"select":       {"project", parseSelect},
		"compute":      {"compute", parseCompute},
		"sort":         {"sort", parseSort},
		"limit":        {"limit", parseLimit},
		"skip":         {"skip", parseSkip},
		"groupby":      {"groupBy", parseGroupBy},
		"aggregate":    {"aggregate", parseAggregate},
		"distinct":     {"distinct", parseDistinct},
		"flatten":      {"flatten", parseFlatten},
		"rename":       {"rename", parseRename},
		"drop":         {"drop", parseDrop},
		"tap":          {"tap", parseTap},
		"callfunction": {"callFunction", parseCallFunction},
		"callapp":      {"callApp", parseCallApp},
		"pivot":        {"pivot", parsePivot},
		"unpivot":      {"unpivot", parseUnpivot},
		"nest":         {"nest", parseNest},
		"unnestobject": {"unnestObject", parseUnnestObject},
	}
	d, ok := dispatch[first]
	if !ok {
		return dsl.PipeStage{}, fmt.Errorf("unknown stage %q", tokens[0].text)
	}
	cfg, err := d.fn(tokens[1:])
	if err != nil {
		return dsl.PipeStage{}, err
	}
	cfg["op"] = d.op
	raw, err := json.Marshal(cfg)
	if err != nil {
		return dsl.PipeStage{}, err
	}
	return dsl.PipeStage{Op: d.op, Config: raw}, nil
}

// --- Per-stage parsers ---

func parseWhere(toks []token) (map[string]any, error) {
	if len(toks) == 0 {
		return nil, fmt.Errorf("where: predicate required")
	}
	// Backtick-raw form: `expr`
	if toks[0].kind == tokRaw {
		return map[string]any{"where": map[string]any{"expr": toks[0].value()}}, nil
	}
	// Simple form: field op value
	if len(toks) < 3 {
		return nil, fmt.Errorf("where: expected `field op value`")
	}
	field := toks[0].value()
	op := toks[1].text
	val, err := tokenValue(toks[2])
	if err != nil {
		return nil, fmt.Errorf("where: %w", err)
	}
	return map[string]any{
		"where": map[string]any{"field": field, "op": op, "value": val},
	}, nil
}

func parseSelect(toks []token) (map[string]any, error) {
	parts := splitOnComma(toks)
	if len(parts) == 0 {
		return nil, fmt.Errorf("select: at least one field required")
	}
	sel := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		f := map[string]any{"field": p[0].value()}
		if len(p) == 3 && strings.ToLower(p[1].text) == "as" {
			f["as"] = p[2].value()
		}
		sel = append(sel, f)
	}
	return map[string]any{"select": sel}, nil
}

func parseCompute(toks []token) (map[string]any, error) {
	if len(toks) < 3 || toks[1].text != "=" {
		return nil, fmt.Errorf("compute: expected `name = expr`")
	}
	as := toks[0].value()
	// Body is everything after `=`, which is either a single backtick-raw token or a literal value.
	if len(toks) == 3 && toks[2].kind == tokRaw {
		return map[string]any{"as": as, "expr": toks[2].value()}, nil
	}
	if len(toks) == 3 && (toks[2].kind == tokString || toks[2].kind == tokNumber) {
		v, err := tokenValue(toks[2])
		if err != nil {
			return nil, err
		}
		// Wrap literal in DTL: just emit it as a constant expr.
		return map[string]any{"as": as, "expr": fmt.Sprintf("%v", v)}, nil
	}
	return nil, fmt.Errorf("compute: expression must be a backtick-quoted DTL expression")
}

func parseSort(toks []token) (map[string]any, error) {
	parts := splitOnComma(toks)
	by := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		key := map[string]any{"field": p[0].value(), "dir": "asc"}
		if len(p) >= 2 {
			d := strings.ToLower(p[1].text)
			if d == "asc" || d == "desc" {
				key["dir"] = d
			}
		}
		by = append(by, key)
	}
	if len(by) == 0 {
		return nil, fmt.Errorf("sort: at least one key required")
	}
	return map[string]any{"by": by}, nil
}

func parseLimit(toks []token) (map[string]any, error) {
	if len(toks) != 1 || toks[0].kind != tokNumber {
		return nil, fmt.Errorf("limit: expected an integer")
	}
	n, _ := strconv.Atoi(toks[0].text)
	return map[string]any{"n": n}, nil
}

func parseSkip(toks []token) (map[string]any, error) {
	if len(toks) != 1 || toks[0].kind != tokNumber {
		return nil, fmt.Errorf("skip: expected an integer")
	}
	n, _ := strconv.Atoi(toks[0].text)
	return map[string]any{"n": n}, nil
}

func parseGroupBy(toks []token) (map[string]any, error) {
	parts := splitOnComma(toks)
	keys := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		keys = append(keys, p[0].value())
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("groupBy: at least one key required")
	}
	return map[string]any{"keys": keys}, nil
}

func parseAggregate(toks []token) (map[string]any, error) {
	parts := splitOnComma(toks)
	aggs := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		// Form: fn(field) as alias
		if len(p) < 4 {
			return nil, fmt.Errorf("aggregate: expected `fn(field) as alias`")
		}
		fn := strings.ToUpper(p[0].text)
		// p[1] should be '(', p[2] field or *, p[3] ')'
		if p[1].text != "(" || p[3].text != ")" {
			return nil, fmt.Errorf("aggregate: expected parentheses around field")
		}
		fieldTok := p[2]
		var field string
		if fieldTok.kind == tokIdent || fieldTok.kind == tokString {
			field = fieldTok.value()
		} else if fieldTok.text == "*" {
			field = "*"
		} else {
			return nil, fmt.Errorf("aggregate: unexpected field token %q", fieldTok.text)
		}
		// 'as' alias
		if len(p) < 6 || strings.ToLower(p[4].text) != "as" {
			return nil, fmt.Errorf("aggregate: missing `as alias`")
		}
		alias := p[5].value()
		aggs = append(aggs, map[string]any{"fn": fn, "field": field, "as": alias})
	}
	if len(aggs) == 0 {
		return nil, fmt.Errorf("aggregate: at least one aggregation required")
	}
	return map[string]any{"aggs": aggs}, nil
}

func parseDistinct(toks []token) (map[string]any, error) {
	if len(toks) == 0 {
		return map[string]any{}, nil
	}
	if strings.ToLower(toks[0].text) != "by" {
		return nil, fmt.Errorf("distinct: expected `by <fields>`")
	}
	parts := splitOnComma(toks[1:])
	keys := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) > 0 {
			keys = append(keys, p[0].value())
		}
	}
	return map[string]any{"by": keys}, nil
}

func parseFlatten(toks []token) (map[string]any, error) {
	if len(toks) == 0 {
		return nil, fmt.Errorf("flatten: field required")
	}
	cfg := map[string]any{"field": toks[0].value()}
	if len(toks) >= 3 && strings.ToLower(toks[1].text) == "as" {
		cfg["as"] = toks[2].value()
	}
	return cfg, nil
}

func parseRename(toks []token) (map[string]any, error) {
	parts := splitOnComma(toks)
	m := make(map[string]string, len(parts))
	for _, p := range parts {
		if len(p) < 3 || p[1].text != "->" {
			return nil, fmt.Errorf("rename: expected `from -> to`")
		}
		m[p[0].value()] = p[2].value()
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("rename: at least one mapping required")
	}
	return map[string]any{"map": m}, nil
}

func parseDrop(toks []token) (map[string]any, error) {
	parts := splitOnComma(toks)
	cols := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) > 0 {
			cols = append(cols, p[0].value())
		}
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("drop: at least one column required")
	}
	return map[string]any{"columns": cols}, nil
}

func parseTap(toks []token) (map[string]any, error) {
	cfg := map[string]any{}
	if len(toks) >= 1 {
		cfg["label"] = toks[0].value()
	}
	return cfg, nil
}

func parseCallFunction(toks []token) (map[string]any, error) {
	if len(toks) == 0 {
		return nil, fmt.Errorf("callFunction: name required")
	}
	cfg := map[string]any{"name": toks[0].value()}
	i := 1
	for i < len(toks) {
		switch strings.ToLower(toks[i].text) {
		case "mode":
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("callFunction: mode value missing")
			}
			cfg["mode"] = toks[i+1].text
			i += 2
		case "pure":
			cfg["pure"] = true
			i++
		case "args":
			if i+1 >= len(toks) || toks[i+1].kind != tokJSON {
				return nil, fmt.Errorf("callFunction: args expects a JSON object")
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(toks[i+1].text), &m); err != nil {
				return nil, fmt.Errorf("callFunction: invalid args JSON: %w", err)
			}
			cfg["args"] = m
			i += 2
		case "as":
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("callFunction: as value missing")
			}
			cfg["as"] = toks[i+1].value()
			i += 2
		default:
			return nil, fmt.Errorf("callFunction: unexpected token %q", toks[i].text)
		}
	}
	return cfg, nil
}

func parseCallApp(toks []token) (map[string]any, error) {
	if len(toks) == 0 {
		return nil, fmt.Errorf("callApp: appId required")
	}
	cfg := map[string]any{"appId": toks[0].value()}
	i := 1
	// Optional .method
	if i < len(toks) && toks[i].text == "." && i+1 < len(toks) {
		cfg["method"] = toks[i+1].value()
		i += 2
	}
	// Optional JSON object payload
	if i < len(toks) && toks[i].kind == tokJSON {
		var m map[string]any
		if err := json.Unmarshal([]byte(toks[i].text), &m); err != nil {
			return nil, fmt.Errorf("callApp: invalid payload JSON: %w", err)
		}
		cfg["payload"] = m
		i++
	}
	if i < len(toks) && strings.ToLower(toks[i].text) == "as" {
		if i+1 >= len(toks) {
			return nil, fmt.Errorf("callApp: as value missing")
		}
		cfg["as"] = toks[i+1].value()
	}
	return cfg, nil
}

// parsePivot accepts the SQL-flavoured form:
//
//	pivot <columnKey>, <aggFn>(<valueField>) for <rowKey1>, <rowKey2>, ...
//
// or with an implicit aggregate (defaults to "first"):
//
//	pivot <columnKey>, <valueField> for <rowKey1>, <rowKey2>, ...
//
// Authors needing fillValue or prefix should use the JSON DSL — those
// modifiers don't fit cleanly into the one-line textual form.
func parsePivot(toks []token) (map[string]any, error) {
	forIdx := indexOfKeyword(toks, "for")
	if forIdx < 0 {
		return nil, fmt.Errorf("pivot: expected `for <rowKey1>[, <rowKey2>...]`")
	}

	head := toks[:forIdx]
	tail := toks[forIdx+1:]

	headParts := splitOnComma(head)
	if len(headParts) < 2 {
		return nil, fmt.Errorf("pivot: expected `<columnKey>, <agg>(<valueField>) for ...`")
	}
	if len(headParts) > 2 {
		return nil, fmt.Errorf("pivot: too many comma-separated parts before `for`")
	}
	if len(headParts[0]) != 1 {
		return nil, fmt.Errorf("pivot: columnKey must be a single identifier")
	}
	columnKey := headParts[0][0].value()

	cfg := map[string]any{"columnKey": columnKey}
	valueExpr := headParts[1]
	switch {
	case len(valueExpr) == 1:
		// Just <valueField>; aggregate defaults to "first" at op level.
		cfg["valueField"] = valueExpr[0].value()
	case len(valueExpr) >= 4 && valueExpr[1].text == "(" && valueExpr[len(valueExpr)-1].text == ")":
		// <fn>(<valueField>) — fn name + parenthesised single field.
		fn := strings.ToLower(valueExpr[0].text)
		inner := valueExpr[2 : len(valueExpr)-1]
		if len(inner) != 1 {
			return nil, fmt.Errorf("pivot: aggregate body must be a single field")
		}
		cfg["aggregate"] = fn
		cfg["valueField"] = inner[0].value()
	default:
		return nil, fmt.Errorf("pivot: expected `<valueField>` or `<agg>(<valueField>)`")
	}

	tailParts := splitOnComma(tail)
	rowKeys := make([]string, 0, len(tailParts))
	for _, p := range tailParts {
		if len(p) == 0 {
			continue
		}
		rowKeys = append(rowKeys, p[0].value())
	}
	if len(rowKeys) == 0 {
		return nil, fmt.Errorf("pivot: at least one rowKey required after `for`")
	}
	cfg["rowKeys"] = rowKeys

	return cfg, nil
}

// parseUnpivot accepts:
//
//	unpivot <col1>, <col2>, ... to (<nameAs>, <valueAs>)
//
// The listed columns are the value columns to melt. idCols default to
// "every column not in the value list" (the runtime op's behaviour). For
// explicit idCols use the JSON DSL.
func parseUnpivot(toks []token) (map[string]any, error) {
	toIdx := indexOfKeyword(toks, "to")
	if toIdx < 0 {
		return nil, fmt.Errorf("unpivot: expected `<col>, <col>, ... to (<nameAs>, <valueAs>)`")
	}

	valueParts := splitOnComma(toks[:toIdx])
	valueCols := make([]string, 0, len(valueParts))
	for _, p := range valueParts {
		if len(p) == 0 {
			continue
		}
		valueCols = append(valueCols, p[0].value())
	}
	if len(valueCols) == 0 {
		return nil, fmt.Errorf("unpivot: at least one value column required before `to`")
	}

	tail := toks[toIdx+1:]
	if len(tail) < 5 || tail[0].text != "(" || tail[len(tail)-1].text != ")" {
		return nil, fmt.Errorf("unpivot: expected `to (<nameAs>, <valueAs>)`")
	}
	inner := tail[1 : len(tail)-1]
	innerParts := splitOnComma(inner)
	if len(innerParts) != 2 || len(innerParts[0]) != 1 || len(innerParts[1]) != 1 {
		return nil, fmt.Errorf("unpivot: `to (...)` expects exactly two identifiers")
	}

	return map[string]any{
		"valueCols": valueCols,
		"nameAs":    innerParts[0][0].value(),
		"valueAs":   innerParts[1][0].value(),
	}, nil
}

// parseNest accepts:
//
//	nest by <key1>, <key2>, ... into <into>
//	nest by <key1>, <key2>, ... into <into> include <col1>, <col2>, ...
func parseNest(toks []token) (map[string]any, error) {
	if len(toks) == 0 || strings.ToLower(toks[0].text) != "by" {
		return nil, fmt.Errorf("nest: expected `by <key1>[, <key2>...] into <field>`")
	}
	intoIdx := indexOfKeyword(toks, "into")
	if intoIdx < 0 {
		return nil, fmt.Errorf("nest: missing `into <field>`")
	}

	keyParts := splitOnComma(toks[1:intoIdx])
	by := make([]string, 0, len(keyParts))
	for _, p := range keyParts {
		if len(p) == 0 {
			continue
		}
		by = append(by, p[0].value())
	}
	if len(by) == 0 {
		return nil, fmt.Errorf("nest: at least one `by` key required")
	}

	rest := toks[intoIdx+1:]
	if len(rest) == 0 {
		return nil, fmt.Errorf("nest: missing field name after `into`")
	}
	cfg := map[string]any{
		"by":   by,
		"into": rest[0].value(),
	}

	rest = rest[1:]
	if len(rest) > 0 {
		if strings.ToLower(rest[0].text) != "include" {
			return nil, fmt.Errorf("nest: unexpected token %q after into-field", rest[0].text)
		}
		incParts := splitOnComma(rest[1:])
		include := make([]string, 0, len(incParts))
		for _, p := range incParts {
			if len(p) == 0 {
				continue
			}
			include = append(include, p[0].value())
		}
		if len(include) == 0 {
			return nil, fmt.Errorf("nest: `include` requires at least one column")
		}
		cfg["include"] = include
	}

	return cfg, nil
}

// parseUnnestObject accepts:
//
//	unnestObject <field>
//	unnestObject <field> prefix <prefix>
//	unnestObject <field> drop
//	unnestObject <field> prefix <prefix> drop
func parseUnnestObject(toks []token) (map[string]any, error) {
	if len(toks) == 0 {
		return nil, fmt.Errorf("unnestObject: field is required")
	}
	cfg := map[string]any{"field": toks[0].value()}
	i := 1
	for i < len(toks) {
		switch strings.ToLower(toks[i].text) {
		case "prefix":
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("unnestObject: prefix value missing")
			}
			cfg["prefix"] = toks[i+1].value()
			i += 2
		case "drop":
			cfg["drop"] = true
			i++
		default:
			return nil, fmt.Errorf("unnestObject: unexpected token %q", toks[i].text)
		}
	}
	return cfg, nil
}

// indexOfKeyword returns the index of the first ident-like token whose
// lowercased text equals kw, ignoring tokens nested inside parentheses.
// Returns -1 when not found. Used by reshape parsers to locate clause
// markers (`for`, `to`, `into`) without false positives from column
// references inside agg expressions.
func indexOfKeyword(toks []token, kw string) int {
	depth := 0
	for i, t := range toks {
		if t.kind == tokPunct {
			switch t.text {
			case "(":
				depth++
			case ")":
				if depth > 0 {
					depth--
				}
			}
			continue
		}
		if depth == 0 && strings.ToLower(t.text) == kw {
			return i
		}
	}
	return -1
}

// --- Token plumbing ---

type tokenKind int

const (
	tokIdent tokenKind = iota
	tokString
	tokNumber
	tokRaw  // backtick-quoted DTL expression
	tokJSON // {...}
	tokOp
	tokPunct
)

type token struct {
	kind tokenKind
	text string // raw text including delimiters where applicable
}

// value returns the meaningful value of a token — string contents without
// quotes, identifier text, or number text. For tokRaw it returns the inside
// of the backticks.
func (t token) value() string {
	switch t.kind {
	case tokString:
		return strings.Trim(t.text, "\"")
	case tokRaw:
		return strings.Trim(t.text, "`")
	default:
		return t.text
	}
}

func tokenValue(t token) (any, error) {
	switch t.kind {
	case tokString:
		return strings.Trim(t.text, "\""), nil
	case tokNumber:
		if strings.Contains(t.text, ".") {
			return strconv.ParseFloat(t.text, 64)
		}
		return strconv.Atoi(t.text)
	case tokIdent:
		switch strings.ToLower(t.text) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		return t.text, nil
	default:
		return t.text, nil
	}
}

func tokenize(s string) []token {
	var out []token
	r := []rune(s)
	i := 0
	for i < len(r) {
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '"':
			j := i + 1
			for j < len(r) && r[j] != '"' {
				if r[j] == '\\' && j+1 < len(r) {
					j += 2
					continue
				}
				j++
			}
			if j < len(r) {
				j++
			}
			out = append(out, token{kind: tokString, text: string(r[i:j])})
			i = j
		case c == '`':
			j := i + 1
			for j < len(r) && r[j] != '`' {
				j++
			}
			if j < len(r) {
				j++
			}
			out = append(out, token{kind: tokRaw, text: string(r[i:j])})
			i = j
		case c == '{':
			depth := 1
			j := i + 1
			for j < len(r) && depth > 0 {
				switch r[j] {
				case '{':
					depth++
				case '}':
					depth--
				}
				j++
			}
			out = append(out, token{kind: tokJSON, text: string(r[i:j])})
			i = j
		case unicode.IsDigit(c) || (c == '-' && i+1 < len(r) && unicode.IsDigit(r[i+1])):
			j := i
			if c == '-' {
				j++
			}
			for j < len(r) && (unicode.IsDigit(r[j]) || r[j] == '.') {
				j++
			}
			out = append(out, token{kind: tokNumber, text: string(r[i:j])})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_' || r[j] == ':' || r[j] == '.') {
				j++
			}
			// Treat tokens with trailing characters as composed names like
			// "math::abs" or "app:slack". The double-colon form pulls in ':'.
			if j == i {
				j = i + 1
			}
			out = append(out, token{kind: tokIdent, text: string(r[i:j])})
			i = j
		case c == '=' || c == '!' || c == '<' || c == '>':
			// Two-char operators
			if i+1 < len(r) && r[i+1] == '=' {
				out = append(out, token{kind: tokOp, text: string(r[i : i+2])})
				i += 2
			} else {
				out = append(out, token{kind: tokOp, text: string(r[i : i+1])})
				i++
			}
		case c == '-':
			if i+1 < len(r) && r[i+1] == '>' {
				out = append(out, token{kind: tokPunct, text: "->"})
				i += 2
			} else {
				out = append(out, token{kind: tokOp, text: "-"})
				i++
			}
		case c == ',' || c == '(' || c == ')' || c == '*' || c == '.':
			out = append(out, token{kind: tokPunct, text: string(c)})
			i++
		default:
			out = append(out, token{kind: tokPunct, text: string(c)})
			i++
		}
	}
	return out
}

// splitOnComma splits a flat token stream on top-level commas.
func splitOnComma(toks []token) [][]token {
	var out [][]token
	var cur []token
	for _, t := range toks {
		if t.kind == tokPunct && t.text == "," {
			out = append(out, cur)
			cur = nil
		} else {
			cur = append(cur, t)
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
