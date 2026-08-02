// Package lang is the editor-facing surface of DQL: completions, diagnostics
// and hover, as pure functions.
//
// Nothing here serves anything. Text and a cursor in, structured results out,
// so the same entry points back an HTTP handler, a WebSocket session, an SSE
// stream or an LSP binary without knowing which.
//
// The work happens in package pipe, which owns the operator catalog and the
// textual parser. This package exists so that DQL and DTL present the same
// vocabulary — Complete, Diagnose, Hover — to anyone wiring editor support for
// both. A tool that has bound one has bound the other.
package lang

import "github.com/xraph/dql/pipe"

// Item is one completion suggestion.
type Item = pipe.CompletionItem

// Diagnostic is one problem found in a document. Lines and columns are
// 1-based, as a compiler reports them; an LSP binding subtracts one.
type Diagnostic = pipe.Diagnostic

// Context carries what the host knows and the language does not: which
// datasets are visible, which functions are registered, which apps exist, and
// the columns of each dataset.
//
// Every field is optional. An empty Context still yields the language itself —
// operator names, keywords and their snippets — which is what an editor with
// no server reachable should get rather than nothing.
type Context = pipe.CompletionContext

// Complete returns the suggestions for a cursor position in text.
func Complete(text string, cursor int, ctx Context) []Item {
	return pipe.CompleteText(text, cursor, ctx)
}

// Diagnose reports the problems in a document.
//
// It is deliberately lenient: a document being typed is usually invalid, and a
// diagnostic pass that gives up at the first error tells an editor nothing
// about the rest of the file.
func Diagnose(text string) []Diagnostic {
	return pipe.DiagnoseText(text)
}

// HoverInfo is documentation for one token.
type HoverInfo struct {
	Word string `json:"word"`
	Doc  string `json:"documentation"`
}

// Hover returns documentation for the operator under the cursor, or nil when
// the cursor is not on one.
func Hover(text string, cursor int, _ Context) *HoverInfo {
	word := wordAt(text, cursor)
	if word == "" {
		return nil
	}
	meta, ok := pipe.CatalogIndex()[word]
	if !ok {
		return nil
	}
	doc := meta.Summary
	if doc == "" {
		doc = meta.Name
	}
	return &HoverInfo{Word: word, Doc: doc}
}

// wordAt extracts the identifier surrounding cursor.
func wordAt(text string, cursor int) string {
	if cursor < 0 || cursor > len(text) {
		return ""
	}
	start := cursor
	for start > 0 && isWordByte(text[start-1]) {
		start--
	}
	end := cursor
	for end < len(text) && isWordByte(text[end]) {
		end++
	}
	return text[start:end]
}

func isWordByte(c byte) bool {
	return c == '_' || c == ':' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
