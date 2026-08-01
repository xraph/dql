package pipe

import (
	"sort"
	"strings"
)

// CompletionItem is a single autocomplete suggestion. Mirrors the shape used
// by the formula extension's LS so frontend code can share rendering.
type CompletionItem struct {
	Label       string `json:"label"`
	Kind        string `json:"kind"` // stage | column | function | app | dataset | keyword | operator | snippet
	Detail      string `json:"detail,omitempty"`
	Description string `json:"description,omitempty"`
	InsertText  string `json:"insertText"`
	SortOrder   int    `json:"sortOrder,omitempty"`
}

// CompletionContext is the position-derived knowledge a CompletionProvider
// needs in addition to its static catalog. Datasets/Functions/Apps/Columns
// are resolved by the controller from the relevant extensions.
type CompletionContext struct {
	Datasets  []string // dataset names visible to the caller
	Functions []string // registered DTL function names (e.g. "math::abs")
	Apps      []string // app IDs
	// Columns is a per-dataset map. Key is dataset name, value is the column
	// list. Empty when not resolvable.
	Columns map[string][]string
}

// CompleteText analyses textual pipe input around `cursor` and returns
// context-aware completions. It is best-effort — when intent is ambiguous
// it returns the broader set rather than nothing.
func CompleteText(text string, cursor int, ctx CompletionContext) []CompletionItem {
	if cursor > len(text) {
		cursor = len(text)
	}
	prefix := text[:cursor]

	// Find the start of the current segment by scanning back for an
	// unescaped, top-level '|'. Anything before the last '|' is committed
	// pipeline state (used for ambient context, not for the cursor word).
	segStart := lastTopLevelPipe(prefix) + 1
	segment := strings.TrimLeftFunc(prefix[segStart:], func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' })
	currentDataset := datasetFromText(text[:segStart])

	tokens := tokenize(segment)
	partial := partialIdent(segment)

	// Source-segment handling: the very first segment must be `source <dataset>`.
	// We are in the first segment when no top-level '|' came before us.
	if segStart == 0 || !hasSourceBefore(prefix) {
		return sourceSegmentCompletions(segment, tokens, partial, ctx)
	}

	// Empty segment (just after `|`) — suggest stage keywords.
	if len(tokens) == 0 || (len(tokens) == 1 && tokens[0].kind == tokIdent && partial == tokens[0].text) {
		return filterAndSort(textualStageItems(), partial)
	}

	kw := strings.ToLower(tokens[0].text)
	switch kw {
	case "where":
		return whereCompletions(tokens[1:], partial, currentDataset, ctx)
	case "select", "project", "drop", "groupby":
		return columnNameItems(currentDataset, partial, ctx)
	case "compute":
		return computeCompletions(tokens[1:], partial, ctx)
	case "sort":
		return sortCompletions(tokens, partial, currentDataset, ctx)
	case "limit", "skip":
		// numeric only — no completions
		return nil
	case "aggregate":
		return aggregateCompletions(tokens, partial, currentDataset, ctx)
	case "distinct":
		// after `by`: column names
		if hasIdent(tokens, "by") {
			return columnNameItems(currentDataset, partial, ctx)
		}
		return []CompletionItem{{Label: "by", Kind: "keyword", InsertText: "by ", Detail: "follow with column list"}}
	case "flatten":
		return columnNameItems(currentDataset, partial, ctx)
	case "rename":
		return renameCompletions(tokens, partial, currentDataset, ctx)
	case "callfunction":
		return callFunctionCompletions(tokens, partial, ctx)
	case "callapp":
		return callAppCompletions(tokens, partial, ctx)
	case "tap":
		return nil
	default:
		// First token isn't a recognised stage keyword — suggest stages.
		return filterAndSort(textualStageItems(), kw)
	}
}

// --- Per-stage completion helpers ---

// textualStages is the subset of the catalog reachable through the textual
// pipe parser (window/branch/merge/lookup are JSON-only). The Keyword field
// is what the user types — most match the op name but `where` maps to filter,
// `select` maps to project.
var textualStages = []struct {
	Keyword string
	Op      string
}{
	{"where", "filter"},
	{"select", "project"},
	{"compute", "compute"},
	{"sort", "sort"},
	{"limit", "limit"},
	{"skip", "skip"},
	{"groupBy", "groupBy"},
	{"aggregate", "aggregate"},
	{"distinct", "distinct"},
	{"flatten", "flatten"},
	{"rename", "rename"},
	{"drop", "drop"},
	{"tap", "tap"},
	{"callFunction", "callFunction"},
	{"callApp", "callApp"},
}

// textualStageItems returns completion items for stage keywords typeable in
// the textual surface. Labels match what the user types; descriptions come
// from the catalog.
func textualStageItems() []CompletionItem {
	idx := CatalogIndex()
	out := make([]CompletionItem, 0, len(textualStages))
	for i, ts := range textualStages {
		meta := idx[ts.Op]
		out = append(out, CompletionItem{
			Label:       ts.Keyword,
			Kind:        "stage",
			Detail:      meta.Summary,
			Description: meta.Description,
			InsertText:  ts.Keyword + " ",
			SortOrder:   i,
		})
	}
	return out
}

// sourceSegmentCompletions handles the very first segment: must be
// `source <dataset>`. Distinguishes "before source typed" from "source
// typed but dataset missing" from "source + dataset present".
func sourceSegmentCompletions(segment string, tokens []token, partial string, ctx CompletionContext) []CompletionItem {
	// 1) Empty segment / partial-source typing: suggest the keyword.
	if len(tokens) == 0 {
		return []CompletionItem{{
			Label:      "source",
			Kind:       "keyword",
			Detail:     "starts a pipe query",
			InsertText: "source ",
		}}
	}
	first := strings.ToLower(tokens[0].text)
	// 2) User is still typing "source": filter the keyword by partial.
	if first != "source" {
		if strings.HasPrefix("source", first) {
			return []CompletionItem{{Label: "source", Kind: "keyword", InsertText: "source "}}
		}
		return nil
	}
	// 3) Source typed; dataset missing or in progress: suggest datasets.
	if len(tokens) == 1 || (len(tokens) == 2 && partial != "") {
		// `tokens[0]` is "source"; everything after it is partial dataset.
		datasetPartial := ""
		if len(tokens) == 2 {
			datasetPartial = tokens[1].text
		}
		return datasetItems(ctx.Datasets, datasetPartial)
	}
	// 4) `source <dataset>` is complete, but no `|` yet — nothing more to
	// add at this position.
	return nil
}

func whereCompletions(toks []token, partial, dataset string, ctx CompletionContext) []CompletionItem {
	// Position 0: column name (from dataset).
	// Position 1: operator.
	// Position 2: value (no completion — too freeform).
	if len(toks) == 0 || (len(toks) == 1 && partial != "") {
		return columnNameItems(dataset, partial, ctx)
	}
	if len(toks) == 1 || (len(toks) == 2 && partial != "") {
		return operatorItems(partial)
	}
	return nil
}

func computeCompletions(toks []token, partial string, ctx CompletionContext) []CompletionItem {
	// `compute name = <expr>` — once `=` seen, suggest backtick start.
	hasEq := false
	for _, t := range toks {
		if t.text == "=" {
			hasEq = true
			break
		}
	}
	if !hasEq {
		return nil // user is typing the alias name
	}
	// After =: suggest backtick template + DTL function names from ctx.
	items := []CompletionItem{
		{Label: "expression", Kind: "snippet", Detail: "DTL expression", InsertText: "`$0`", SortOrder: 0},
	}
	for _, fn := range ctx.Functions {
		items = append(items, CompletionItem{
			Label:      fn,
			Kind:       "function",
			Detail:     "DTL function",
			InsertText: "`" + fn + "($0)`",
			SortOrder:  10,
		})
	}
	return filterAndSort(items, partial)
}

func sortCompletions(toks []token, partial, dataset string, ctx CompletionContext) []CompletionItem {
	// First non-keyword token: column name. Subsequent tokens: asc / desc.
	hasField := false
	for _, t := range toks[1:] { // skip "sort"
		if t.kind == tokIdent && t.text != "asc" && t.text != "desc" {
			hasField = true
			break
		}
	}
	if !hasField {
		return columnNameItems(dataset, partial, ctx)
	}
	return []CompletionItem{
		{Label: "asc", Kind: "keyword", InsertText: "asc"},
		{Label: "desc", Kind: "keyword", InsertText: "desc"},
	}
}

func aggregateCompletions(toks []token, partial, dataset string, ctx CompletionContext) []CompletionItem {
	// Inside `aggregate <fn>(<field>) as <alias>`. Distinguish based on
	// whether the cursor is inside the parens.
	insideParens := tokensCount(toks, "(") > tokensCount(toks, ")")
	if insideParens {
		items := columnNameItems(dataset, partial, ctx)
		items = append(items, CompletionItem{Label: "*", Kind: "keyword", InsertText: "*"})
		return items
	}
	// Otherwise: aggregate function names.
	fns := []string{"count", "sum", "avg", "min", "max"}
	out := make([]CompletionItem, 0, len(fns))
	for _, fn := range fns {
		out = append(out, CompletionItem{
			Label:      fn,
			Kind:       "function",
			Detail:     "aggregate function",
			InsertText: fn + "($1) as $2",
		})
	}
	return filterAndSort(out, partial)
}

func renameCompletions(toks []token, partial, dataset string, ctx CompletionContext) []CompletionItem {
	// rename <from> -> <to>, ...
	// After arrows, the `to` is freeform; before arrows, suggest column names.
	hasArrow := false
	for _, t := range toks {
		if t.text == "->" {
			hasArrow = true
		}
	}
	if hasArrow {
		return nil
	}
	return columnNameItems(dataset, partial, ctx)
}

func callFunctionCompletions(toks []token, partial string, ctx CompletionContext) []CompletionItem {
	// First arg is the function name. Then optional `mode|pure|args|as` keywords.
	if len(toks) <= 1 {
		// Suggest function names.
		out := make([]CompletionItem, 0, len(ctx.Functions))
		for _, fn := range ctx.Functions {
			out = append(out, CompletionItem{
				Label:      fn,
				Kind:       "function",
				Detail:     "DTL function",
				InsertText: fn,
			})
		}
		return filterAndSort(out, partial)
	}
	return []CompletionItem{
		{Label: "mode", Kind: "keyword", InsertText: "mode perRow", Detail: "perRow | batch"},
		{Label: "pure", Kind: "keyword", InsertText: "pure", Detail: "declare pure → live-safe"},
		{Label: "args", Kind: "keyword", InsertText: "args {$0}", Detail: "JSON args object"},
		{Label: "as", Kind: "keyword", InsertText: "as $0"},
	}
}

func callAppCompletions(toks []token, partial string, ctx CompletionContext) []CompletionItem {
	if len(toks) <= 1 {
		out := make([]CompletionItem, 0, len(ctx.Apps))
		for _, app := range ctx.Apps {
			out = append(out, CompletionItem{
				Label:      app,
				Kind:       "app",
				Detail:     "managed app",
				InsertText: app,
			})
		}
		return filterAndSort(out, partial)
	}
	return nil
}

// --- Generic helpers ---

func columnNameItems(dataset, partial string, ctx CompletionContext) []CompletionItem {
	cols := ctx.Columns[dataset]
	out := make([]CompletionItem, 0, len(cols))
	for _, c := range cols {
		out = append(out, CompletionItem{Label: c, Kind: "column", InsertText: c})
	}
	return filterAndSort(out, partial)
}

func operatorItems(partial string) []CompletionItem {
	ops := []string{"==", "!=", ">", "<", ">=", "<=", "in", "not_in", "like", "is_null", "is_not_null"}
	out := make([]CompletionItem, 0, len(ops))
	for _, op := range ops {
		out = append(out, CompletionItem{Label: op, Kind: "operator", InsertText: op})
	}
	return filterAndSort(out, partial)
}

func datasetItems(datasets []string, partial string) []CompletionItem {
	out := make([]CompletionItem, 0, len(datasets))
	for _, d := range datasets {
		// App-backed source refs get their own kind so editors can
		// distinguish live app data from stored datasets.
		kind := "dataset"
		if strings.HasPrefix(d, "app:") {
			kind = "app"
		}
		out = append(out, CompletionItem{Label: d, Kind: kind, InsertText: d})
	}
	return filterAndSort(out, partial)
}

// --- Cursor / token utilities ---

// lastTopLevelPipe returns the index of the rightmost '|' that is not inside
// a string, raw expression, or balanced bracket. Returns -1 if none.
func lastTopLevelPipe(s string) int {
	depthParen, depthBracket, depthBrace := 0, 0, 0
	inString := false
	inRaw := false
	escape := false
	last := -1
	for i, r := range s {
		if escape {
			escape = false
			continue
		}
		switch {
		case inString:
			switch r {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
		case inRaw:
			if r == '`' {
				inRaw = false
			}
		default:
			switch r {
			case '"':
				inString = true
			case '`':
				inRaw = true
			case '(':
				depthParen++
			case ')':
				depthParen--
			case '[':
				depthBracket++
			case ']':
				depthBracket--
			case '{':
				depthBrace++
			case '}':
				depthBrace--
			case '|':
				if depthParen == 0 && depthBracket == 0 && depthBrace == 0 {
					last = i
				}
			}
		}
	}
	return last
}

// hasSourceBefore returns true when `source <name>` appears in the prefix.
func hasSourceBefore(prefix string) bool {
	low := strings.ToLower(prefix)
	idx := strings.Index(low, "source")
	if idx < 0 {
		return false
	}
	// Require a word boundary after "source".
	after := idx + len("source")
	if after >= len(low) {
		return false
	}
	switch low[after] {
	case ' ', '\t', '\n':
		return true
	}
	return false
}

// datasetFromText extracts the dataset name from `source <name>` if present.
func datasetFromText(text string) string {
	low := strings.ToLower(text)
	idx := strings.Index(low, "source")
	if idx < 0 {
		return ""
	}
	rest := text[idx+len("source"):]
	rest = strings.TrimLeft(rest, " \t\n")
	end := 0
	for end < len(rest) && !isSeparator(rest[end]) {
		end++
	}
	return rest[:end]
}

func isSeparator(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '|', ',':
		return true
	}
	return false
}

// partialIdent returns the trailing identifier the user is typing.
func partialIdent(seg string) string {
	end := len(seg)
	start := end
	for start > 0 {
		c := seg[start-1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == ':' || c == '.' {
			start--
		} else {
			break
		}
	}
	return seg[start:end]
}

func tokensCount(toks []token, text string) int {
	n := 0
	for _, t := range toks {
		if t.text == text {
			n++
		}
	}
	return n
}

func hasIdent(toks []token, text string) bool {
	for _, t := range toks {
		if strings.EqualFold(t.text, text) {
			return true
		}
	}
	return false
}

// filterAndSort drops items whose label doesn't prefix-match the partial,
// then sorts by SortOrder ascending then label.
func filterAndSort(items []CompletionItem, partial string) []CompletionItem {
	out := items
	if partial != "" {
		low := strings.ToLower(partial)
		out = out[:0]
		for _, it := range items {
			if strings.HasPrefix(strings.ToLower(it.Label), low) {
				out = append(out, it)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].Label < out[j].Label
	})
	return out
}
