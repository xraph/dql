package main

import (
	"strings"
	"testing"

	"github.com/xraph/langserver/lsp"
)

// Driven the way an editor drives it — framed messages in, framed messages out
// — because a handler bound to the wrong method name, or answering with a
// shape the protocol does not expect, passes a unit test and fails in an
// editor.

func TestInitialize_advertisesWhatItImplements(t *testing.T) {
	msgs := newConversation(t).send(req(1, "initialize", "{}"))

	if len(msgs) != 1 {
		t.Fatalf("want one response, got %d", len(msgs))
	}
	caps, ok := msgs[0]["result"].(map[string]any)["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("no capabilities in %v", msgs[0])
	}
	// Advertising something unimplemented makes an editor send requests that
	// go unanswered, which presents as a hang.
	if caps["hoverProvider"] != true {
		t.Error("hover is implemented but not advertised")
	}
	if _, found := caps["completionProvider"]; !found {
		t.Error("completion is implemented but not advertised")
	}
	if caps["textDocumentSync"] != float64(1) {
		t.Errorf("textDocumentSync = %v, want 1 (full)", caps["textDocumentSync"])
	}
}

func TestDidOpen_publishesDiagnosticsForABrokenDocument(t *testing.T) {
	c := newConversation(t)
	msgs := c.send(note("textDocument/didOpen",
		`{"textDocument":{"uri":"file:///a.dql","text":"source events | nosuchoperator foo"}}`))

	if len(msgs) == 0 {
		t.Fatal("opening a broken document published nothing")
	}
	if msgs[0]["method"] != "textDocument/publishDiagnostics" {
		t.Fatalf("expected diagnostics, got %v", msgs[0]["method"])
	}
	diags := msgs[0]["params"].(map[string]any)["diagnostics"].([]any)
	if len(diags) == 0 {
		t.Error("an unknown operator produced no diagnostics")
	}
}

// Diagnostic positions come from the language 1-based and go out 0-based.
// An off-by-one here puts every squiggle on the wrong line, which looks like
// the diagnostics are simply wrong rather than misplaced.
func TestDiagnostics_positionsAreZeroBased(t *testing.T) {
	c := newConversation(t)
	msgs := c.send(note("textDocument/didOpen",
		`{"textDocument":{"uri":"file:///a.dql","text":"source events | nosuchoperator foo"}}`))

	diags := msgs[0]["params"].(map[string]any)["diagnostics"].([]any)
	if len(diags) == 0 {
		t.Skip("no diagnostics to inspect")
	}
	for _, d := range diags {
		rng := d.(map[string]any)["range"].(map[string]any)
		start := rng["start"].(map[string]any)
		if start["line"].(float64) < 0 || start["character"].(float64) < 0 {
			t.Errorf("negative position %v — the 1-based to 0-based conversion underflowed", start)
		}
	}
}

func TestCompletion_offersTheLanguageOffline(t *testing.T) {
	// No platform attached: no datasets, no functions, no apps. The operator
	// vocabulary must still be there, or a .dql file on disk gets nothing.
	c := newConversation(t)
	msgs := c.send(
		note("textDocument/didOpen", `{"textDocument":{"uri":"file:///a.dql","text":"source events | "}}`),
		req(2, "textDocument/completion",
			`{"textDocument":{"uri":"file:///a.dql"},"position":{"line":0,"character":16}}`),
	)

	var items []any
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok {
			items, _ = res["items"].([]any)
		}
	}
	if len(items) == 0 {
		t.Fatal("no completions with no platform attached — the offline case is the point")
	}
}

func TestCompletion_onAnUnopenedDocumentIsAnError(t *testing.T) {
	msgs := newConversation(t).send(req(1, "textDocument/completion",
		`{"textDocument":{"uri":"file:///never-opened.dql"},"position":{"line":0,"character":0}}`))

	if len(msgs) != 1 {
		t.Fatalf("want one response, got %d", len(msgs))
	}
	if _, hasErr := msgs[0]["error"]; !hasErr {
		t.Error("a request for an unknown document should report an error, not empty results")
	}
}

// The UTF-16 conversion is invisible in ASCII and wrong everywhere else. The
// position is derived rather than hand-counted: counting code units by eye is
// the error this guards against, and doing it in the test just relocates it.
func TestCompletion_positionSurvivesMultiByteText(t *testing.T) {
	src := `source events | filter status == "🌍" | limit 10`
	cursor := strings.Index(src, "limit") + len("limit")
	pos := lsp.PositionFromOffset(src, cursor)

	c := newConversation(t)
	msgs := c.send(
		note("textDocument/didOpen", `{"textDocument":{"uri":"file:///e.dql","text":`+jsonString(src)+`}}`),
		req(2, "textDocument/hover",
			`{"textDocument":{"uri":"file:///e.dql"},"position":{"line":`+itoa(pos.Line)+`,"character":`+itoa(pos.Character)+`}}`),
	)

	var hovered bool
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok && res != nil {
			if contents, ok := res["contents"].(map[string]any); ok {
				if v, _ := contents["value"].(string); v != "" {
					hovered = true
				}
			}
		}
	}
	if !hovered {
		t.Error("hover on `limit` returned nothing — the position resolved to the wrong token")
	}
}

func TestDidClose_clearsDiagnostics(t *testing.T) {
	c := newConversation(t)
	c.send(note("textDocument/didOpen",
		`{"textDocument":{"uri":"file:///a.dql","text":"source events | nosuchoperator foo"}}`))

	msgs := c.send(note("textDocument/didClose", `{"textDocument":{"uri":"file:///a.dql"}}`))
	if len(msgs) == 0 {
		t.Fatal("closing published nothing — the errors would stay in the gutter")
	}
	diags := msgs[0]["params"].(map[string]any)["diagnostics"].([]any)
	if len(diags) != 0 {
		t.Errorf("close should clear diagnostics, got %v", diags)
	}
}

// A valid document in the other surface form must not be reported as a broken
// textual query. Before this was handled, every real .dql file lit up with
// "query must start with `source <dataset>`" on open.
func TestDidOpen_documentFormIsNotFalselyFlagged(t *testing.T) {
	c := newConversation(t)
	msgs := c.send(note("textDocument/didOpen",
		`{"textDocument":{"uri":"file:///doc.dql","text":"from:\n  dataset: events\nwhere:\n  field: status\n  op: \"==\"\n  value: open\n"}}`))

	if len(msgs) == 0 {
		t.Fatal("nothing published for a document-form file")
	}
	diags := msgs[0]["params"].(map[string]any)["diagnostics"].([]any)
	if len(diags) != 0 {
		t.Errorf("a valid document was reported as broken: %v", diags)
	}
}

func TestLooksTextual(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"textual query", `source events | limit 10`, true},
		{"yaml document", "from:\n  dataset: events\n", false},
		{"json document", `{"from":{"dataset":"events"}}`, false},
		{"textual after a comment", "# a note\nsource events", true},
		{"yaml after a comment", "# a note\nfrom:\n  dataset: x", false},
		{"empty", "", false},
		{"comments only", "# nothing here\n\n", false},
	} {
		if got := looksTextual(tc.src); got != tc.want {
			t.Errorf("%s: looksTextual = %v, want %v", tc.name, got, tc.want)
		}
	}
}
