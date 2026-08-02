package lang

import (
	"strings"
	"testing"
)

// The point of this package is that an editor with nothing behind it still
// gets the language. If an empty Context yields nothing, the offline case —
// a .dql file open on disk — is dead.
func TestComplete_emptyContextStillOffersTheLanguage(t *testing.T) {
	items := Complete("", 0, Context{})
	if len(items) == 0 {
		t.Fatal("an empty context produced no completions")
	}
}

func TestDiagnose_flagsABrokenDocument(t *testing.T) {
	diags := Diagnose("from: \n  dataset:\npipe:\n  - op: nosuchoperator\n")
	if len(diags) == 0 {
		t.Error("a document naming an unknown operator produced no diagnostics")
	}
}

// Diagnosis has to survive a half-typed document, because that is the state a
// document spends most of its life in. Giving up at the first error would tell
// an editor nothing about the rest of the file.
func TestDiagnose_toleratesAPartialDocument(t *testing.T) {
	for _, src := range []string{"", "from:", "from:\n  dataset: events\npipe:\n  - op: ", "  \n\n"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Diagnose(%q) panicked: %v", src, r)
				}
			}()
			Diagnose(src)
		}()
	}
}

func TestHover_describesAnOperator(t *testing.T) {
	src := "pipe:\n  - op: filter\n"
	cursor := strings.Index(src, "filter") + 2

	info := Hover(src, cursor, Context{})
	if info == nil {
		t.Fatal("no hover on a known operator")
	}
	if info.Word != "filter" {
		t.Errorf("word = %q, want filter", info.Word)
	}
	if info.Doc == "" {
		t.Error("operator hover carried no documentation")
	}
}

func TestHover_nilOffAnOperator(t *testing.T) {
	if info := Hover("from:\n  dataset: events\n", 3, Context{}); info != nil {
		t.Errorf("hover on a non-operator should be nil, got %+v", info)
	}
	if info := Hover("   ", 1, Context{}); info != nil {
		t.Errorf("hover on whitespace should be nil, got %+v", info)
	}
}

// Cursor positions arrive from an editor and can be stale or malformed. None
// of them should panic — a language server that crashes takes the editor's
// features down with it.
func TestHover_outOfRangeCursorIsSafe(t *testing.T) {
	src := "pipe:\n  - op: filter\n"
	for _, cursor := range []int{-1, 0, len(src), len(src) + 100} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Hover(cursor=%d) panicked: %v", cursor, r)
				}
			}()
			Hover(src, cursor, Context{})
		}()
	}
}
