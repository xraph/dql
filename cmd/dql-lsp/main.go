// Command dql-lsp is a Language Server Protocol server for DQL's textual pipe
// syntax:
//
//	source events | filter status == "open" | limit 10
//
// It speaks LSP over stdin and stdout, which is how editors launch a language
// server, and answers from dql/lang.
//
// # Which syntax
//
// DQL has two surface forms. Documents — the YAML and JSON that .dql files and
// stored queries use — and the textual pipe syntax above. The language
// intelligence in dql/lang covers the textual form only, so this server
// diagnoses only that: handed a document, its parser reports "query must start
// with `source <dataset>`", which is true of the textual grammar and useless
// as feedback on a perfectly valid YAML file.
//
// Rather than emit an error on every valid document, the server recognises the
// document form and stays quiet. Completions and hover are still offered,
// because they degrade to nothing rather than to something wrong. Document-form
// intelligence is a gap, not a decision.
//
// It holds no connection to any platform, so it works on a .dql file on disk
// with nothing else running. What it cannot know offline is what a particular
// deployment contains: dataset names, registered functions, app ids. Those are
// completions a hosted endpoint adds by filling a richer Context; the language
// itself — operators, keywords, their arguments — is always available.
//
//	go install github.com/xraph/dql/cmd/dql-lsp@latest
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/xraph/dql/lang"
	"github.com/xraph/langserver"
	"github.com/xraph/langserver/lsp"
	"github.com/xraph/langserver/stdio"
)

type server struct {
	docs *docs
	conn *stdio.Conn
}

func main() {
	s := &server{docs: newDocs()}
	s.conn = stdio.NewConn(os.Stdin, os.Stdout, langserver.NewBaseSession("dql-lsp", ""))

	if err := s.conn.Serve(s.methods()); err != nil {
		fmt.Fprintf(os.Stderr, "dql-lsp: %v\n", err)
		os.Exit(1)
	}
}

func (s *server) methods() map[string]langserver.Handler {
	return map[string]langserver.Handler{
		"initialize":              s.initialize,
		"initialized":             noop,
		"shutdown":                func(langserver.Session, json.RawMessage) (any, *langserver.RPCError) { return nil, nil },
		"exit":                    noop,
		"textDocument/didOpen":    s.didOpen,
		"textDocument/didChange":  s.didChange,
		"textDocument/didClose":   s.didClose,
		"textDocument/completion": s.completion,
		"textDocument/hover":      s.hover,
	}
}

func noop(langserver.Session, json.RawMessage) (any, *langserver.RPCError) { return nil, nil }

func (s *server) initialize(_ langserver.Session, _ json.RawMessage) (any, *langserver.RPCError) {
	return map[string]any{
		"capabilities": map[string]any{
			// Full document sync. Query documents are small, and incremental
			// sync buys nothing but a class of state-drift bugs.
			"textDocumentSync": 1,
			"completionProvider": map[string]any{
				// A colon opens a mapping value and a dash opens a list item;
				// both are where a suggestion is wanted in this document shape.
				"triggerCharacters": []string{":", "-", " "},
			},
			"hoverProvider": true,
		},
		"serverInfo": map[string]any{"name": "dql-lsp"},
	}, nil
}

type didOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

func (s *server) didOpen(_ langserver.Session, raw json.RawMessage) (any, *langserver.RPCError) {
	var p didOpenParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, badParams(err)
	}
	s.docs.set(p.TextDocument.URI, p.TextDocument.Text)
	s.publishDiagnostics(p.TextDocument.URI, p.TextDocument.Text)
	return nil, nil
}

type didChangeParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

func (s *server) didChange(_ langserver.Session, raw json.RawMessage) (any, *langserver.RPCError) {
	var p didChangeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, badParams(err)
	}
	if len(p.ContentChanges) == 0 {
		return nil, nil
	}
	text := p.ContentChanges[len(p.ContentChanges)-1].Text
	s.docs.set(p.TextDocument.URI, text)
	s.publishDiagnostics(p.TextDocument.URI, text)
	return nil, nil
}

func (s *server) didClose(_ langserver.Session, raw json.RawMessage) (any, *langserver.RPCError) {
	var p didOpenParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, badParams(err)
	}
	s.docs.remove(p.TextDocument.URI)
	// An empty list, not silence: diagnostics persist in the gutter until
	// something replaces them.
	_ = s.conn.Notify("textDocument/publishDiagnostics", map[string]any{
		"uri": p.TextDocument.URI, "diagnostics": []any{},
	})
	return nil, nil
}

type positionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lsp.Position `json:"position"`
}

func (s *server) completion(_ langserver.Session, raw json.RawMessage) (any, *langserver.RPCError) {
	text, cursor, rpcErr := s.locate(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}

	items := lang.Complete(text, cursor, lang.Context{})

	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		entry := map[string]any{"label": it.Label, "kind": lspKind(it.Kind)}
		if it.Detail != "" {
			entry["detail"] = it.Detail
		}
		if it.Description != "" {
			entry["documentation"] = it.Description
		}
		if it.InsertText != "" {
			entry["insertText"] = it.InsertText
		}
		out = append(out, entry)
	}
	return map[string]any{"isIncomplete": false, "items": out}, nil
}

func (s *server) hover(_ langserver.Session, raw json.RawMessage) (any, *langserver.RPCError) {
	text, cursor, rpcErr := s.locate(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}

	info := lang.Hover(text, cursor, lang.Context{})
	if info == nil {
		// null, not an empty hover — editors render an empty box for the latter.
		return nil, nil
	}
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": info.Doc},
	}, nil
}

// locate turns a position request into document text and a byte offset. This
// is where LSP's UTF-16 character index becomes a Go offset.
func (s *server) locate(raw json.RawMessage) (string, int, *langserver.RPCError) {
	var p positionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", 0, badParams(err)
	}
	text, ok := s.docs.get(p.TextDocument.URI)
	if !ok {
		return "", 0, &langserver.RPCError{
			Code:    -32602,
			Message: "document is not open: " + p.TextDocument.URI,
		}
	}
	return text, lsp.OffsetFromPosition(text, p.Position), nil
}

func (s *server) publishDiagnostics(uri, text string) {
	diags := make([]map[string]any, 0)

	// Only the textual form is diagnosable. A document would otherwise be
	// reported as a broken textual query on every keystroke.
	if !looksTextual(text) {
		_ = s.conn.Notify("textDocument/publishDiagnostics", map[string]any{
			"uri": uri, "diagnostics": diags,
		})
		return
	}

	for _, d := range lang.Diagnose(text) {
		severity := 1 // error
		switch d.Severity {
		case "warning":
			severity = 2
		case "info":
			severity = 3
		}
		// The language reports 1-based positions, as a compiler does; LSP is
		// zero-based.
		start := map[string]any{"line": zeroBased(d.Line), "character": zeroBased(d.Column)}
		end := start
		if d.EndLine > 0 {
			end = map[string]any{"line": zeroBased(d.EndLine), "character": zeroBased(d.EndColumn)}
		}
		diags = append(diags, map[string]any{
			"range":    map[string]any{"start": start, "end": end},
			"severity": severity,
			"code":     d.Code,
			"source":   "dql",
			"message":  d.Message,
		})
	}

	_ = s.conn.Notify("textDocument/publishDiagnostics", map[string]any{
		"uri": uri, "diagnostics": diags,
	})
}

func zeroBased(n int) int {
	if n <= 0 {
		return 0
	}
	return n - 1
}

// lspKind maps the language's completion kinds onto LSP's numeric enum.
func lspKind(kind string) int {
	switch kind {
	case "stage", "operator":
		return 3 // function
	case "column", "dataset":
		return 6 // variable
	case "function":
		return 3
	case "app":
		return 9 // module
	case "keyword":
		return 14
	case "snippet":
		return 15
	default:
		return 1 // text
	}
}

func badParams(err error) *langserver.RPCError {
	return &langserver.RPCError{Code: -32602, Message: "invalid params: " + err.Error()}
}

// looksTextual reports whether text is the pipe syntax rather than a YAML or
// JSON document.
//
// The test is the first meaningful character: a document form opens a mapping
// key or a brace, the textual form opens with a word. Comments and blank lines
// are skipped so a file with a header comment is still classified correctly.
func looksTextual(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") {
			return false
		}
		// A mapping key: a bare word followed by a colon, with no pipe before
		// it. `source events | ...` has no colon; `from:` does.
		if i := strings.IndexByte(line, ':'); i > 0 && !strings.Contains(line[:i], "|") {
			return false
		}
		return true
	}
	// Empty or comments only: nothing to be wrong about.
	return false
}
