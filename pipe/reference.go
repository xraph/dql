package pipe

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Reference renders the operator catalog as Markdown.
//
// The catalog already carries every fact a reader needs — summary, description,
// config schema, examples, requirements — so the documentation is generated
// from it rather than written beside it. Thirty-nine operators maintained by
// hand in two places drift apart within a release; generated, they cannot.
//
// Hosts can call this to publish their own operator page. The repository's
// docs/OPERATORS.md is this function's output, held in sync by a test.
//
//go:generate go test -run TestReference_committedFileIsCurrent -args -update-docs
func Reference() string {
	var b strings.Builder

	b.WriteString("# DQL pipe operators\n\n")
	b.WriteString("Every operator in the pipe catalog. **This file is generated** from\n")
	b.WriteString("`pipe.Reference()` — edit `pipe/catalog.go` and run `make generate`.\n\n")
	b.WriteString("A pipe is an ordered chain of these operators applied to a stream of rows.\n")
	b.WriteString("They appear below in catalog order, which follows the shape of a typical\n")
	b.WriteString("pipe: source, filter, transform, aggregate, sort, side effect.\n\n")

	cat := Catalog()

	b.WriteString("## Column key\n\n")
	b.WriteString("| Column | Meaning |\n|---|---|\n")
	b.WriteString("| **Live-safe** | Pure and deterministic with default config, so it can run on a live-updating result set. |\n")
	b.WriteString("| **Pushable** | Can fold into the SQL prefix. The planner makes the final call from the field types. |\n")
	b.WriteString("| **Requires** | Host services the operator needs in its `OpContext`. Blank means it works anywhere. |\n\n")

	b.WriteString("## Index\n\n")
	b.WriteString("| Operator | Summary | Live-safe | Pushable | Requires |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, m := range cat {
		fmt.Fprintf(&b, "| [`%s`](#%s) | %s | %s | %s | %s |\n",
			m.Name, anchor(m.Name), escapeCell(m.Summary),
			checkmark(m.LiveSafeDefault), checkmark(m.Pushable), requiresCell(m.Requires))
	}
	b.WriteString("\n---\n\n")

	for _, m := range cat {
		writeOperator(&b, m)
	}

	return b.String()
}

func writeOperator(b *strings.Builder, m OpMetadata) {
	fmt.Fprintf(b, "## `%s`\n\n", m.Name)
	if m.Summary != "" {
		fmt.Fprintf(b, "%s\n\n", m.Summary)
	}

	fmt.Fprintf(b, "*Live-safe by default:* %s &nbsp;&nbsp; *Pushable:* %s",
		yesNo(m.LiveSafeDefault), yesNo(m.Pushable))
	if len(m.Requires) > 0 {
		fmt.Fprintf(b, " &nbsp;&nbsp; *Requires:* %s", requiresCell(m.Requires))
	}
	b.WriteString("\n\n")

	if d := strings.TrimSpace(m.Description); d != "" {
		fmt.Fprintf(b, "%s\n\n", d)
	}

	writeConfig(b, m.ConfigSchema)

	for _, ex := range m.Examples {
		title := ex.Title
		if title == "" {
			title = "Example"
		}
		fmt.Fprintf(b, "**%s**\n\n```json\n%s\n```\n\n", title, indentJSON(ex.Config))
	}

	b.WriteString("---\n\n")
}

func writeConfig(b *strings.Builder, schema map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		b.WriteString("Takes no configuration.\n\n")
		return
	}

	required := map[string]bool{}
	if rs, ok := schema["required"].([]any); ok {
		for _, r := range rs {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	if rs, ok := schema["required"].([]string); ok {
		for _, r := range rs {
			required[r] = true
		}
	}

	b.WriteString("| Field | Type | Required | Description |\n|---|---|---|---|\n")
	for _, name := range propertyOrder(schema, props) {
		p, _ := props[name].(map[string]any)
		req := ""
		if required[name] {
			req = "yes"
		} else if when, ok := p["x-dql-required-when"]; ok {
			req = "conditional " + escapeCell(compactJSON(when))
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n",
			name, schemaType(p), req, escapeCell(description(p)))
	}
	b.WriteString("\n")
}

// propertyOrder honours the catalog's x-dql-property-order, which exists
// because JSON object key order is not preserved. Fields the hint omits follow
// alphabetically, so a newly added field appears rather than vanishing.
func propertyOrder(schema map[string]any, props map[string]any) []string {
	var out []string
	seen := map[string]bool{}

	appendIfPresent := func(name string) {
		if _, ok := props[name]; ok && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	switch hint := schema["x-dql-property-order"].(type) {
	case []string:
		for _, n := range hint {
			appendIfPresent(n)
		}
	case []any:
		for _, n := range hint {
			if s, ok := n.(string); ok {
				appendIfPresent(s)
			}
		}
	}

	rest := make([]string, 0, len(props))
	for name := range props {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func schemaType(p map[string]any) string {
	if p == nil {
		return ""
	}
	if enum, ok := p["enum"].([]any); ok && len(enum) > 0 {
		vals := make([]string, 0, len(enum))
		for _, e := range enum {
			vals = append(vals, fmt.Sprintf("`%v`", e))
		}
		return strings.Join(vals, " \\| ")
	}
	t, _ := p["type"].(string)
	if t == "array" {
		if items, ok := p["items"].(map[string]any); ok {
			if it, ok := items["type"].(string); ok {
				return t + " of " + it
			}
		}
	}
	if t == "" {
		return "any"
	}
	return t
}

func description(p map[string]any) string {
	if p == nil {
		return ""
	}
	d, _ := p["description"].(string)
	if kind, ok := p["x-dql-input"].(string); ok && kind != "" {
		if d != "" {
			d += " "
		}
		d += "(input: " + kind + ")"
	}
	if def, ok := p["default"]; ok {
		if d != "" {
			d += " "
		}
		d += fmt.Sprintf("Default: `%v`.", def)
	}
	return d
}

func requiresCell(rs []Requirement) string {
	if len(rs) == 0 {
		return ""
	}
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, "`"+string(r)+"`")
	}
	return strings.Join(out, ", ")
}

func checkmark(v bool) string {
	if v {
		return "✅"
	}
	return "—"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// escapeCell keeps generated text inside its Markdown table cell. A pipe in a
// description would otherwise split the row and silently shift every column.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}

// anchor mirrors how GitHub derives heading ids, so the index links resolve.
// Each operator heading wraps the name in backticks, which GitHub strips before
// lowercasing what remains.
func anchor(name string) string {
	return strings.ToLower(name)
}

func indentJSON(v any) string { return marshalDoc(v, "  ") }

func compactJSON(v any) string { return marshalDoc(v, "") }

// marshalDoc renders JSON for human reading. The stdlib escapes the angle
// brackets and ampersand into unicode sequences, which is right for embedding
// JSON in HTML and wrong for a code block a person reads: a DTL comparison
// would arrive with its greater-than sign spelled out as an escape.
func marshalDoc(v any, indent string) string {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimRight(buf.String(), "\n")
}
