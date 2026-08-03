package pipe

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateDocs = flag.Bool("update-docs", false, "rewrite docs/OPERATORS.md from the catalog")

const referencePath = "../docs/OPERATORS.md"

// The committed reference must match what the catalog produces. Adding an
// operator or editing a description without regenerating leaves the published
// docs describing a language that no longer exists — the failure mode this
// test exists to make loud.
func TestReference_committedFileIsCurrent(t *testing.T) {
	got := Reference()

	if *updateDocs {
		if err := os.MkdirAll(filepath.Dir(referencePath), 0o755); err != nil {
			t.Fatalf("create docs dir: %v", err)
		}
		if err := os.WriteFile(referencePath, []byte(got), 0o644); err != nil {
			t.Fatalf("write reference: %v", err)
		}
		t.Logf("wrote %s", referencePath)
		return
	}

	want, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatalf("read %s: %v\nrun: make generate", referencePath, err)
	}
	if string(want) != got {
		t.Errorf("%s is stale — the catalog has changed since it was generated.\nrun: make generate", referencePath)
	}
}

func TestReference_documentsEveryOperator(t *testing.T) {
	ref := Reference()
	for _, m := range Catalog() {
		if !strings.Contains(ref, "## `"+m.Name+"`") {
			t.Errorf("%s has no section in the reference", m.Name)
		}
		if !strings.Contains(ref, "[`"+m.Name+"`](#"+anchor(m.Name)+")") {
			t.Errorf("%s is missing from the index", m.Name)
		}
	}
}

// Requirements belong in the docs for the same reason they are in the catalog:
// a reader choosing an operator needs to know it will not run everywhere.
func TestReference_showsRequirements(t *testing.T) {
	ref := Reference()
	for _, m := range Catalog() {
		if len(m.Requires) == 0 {
			continue
		}
		section := sectionFor(t, ref, m.Name)
		for _, r := range m.Requires {
			if !strings.Contains(section, "`"+string(r)+"`") {
				t.Errorf("%s requires %q but its section does not say so", m.Name, r)
			}
		}
	}
}

// A stray pipe in a description would silently shift every column of the row
// it lands in, and the table would still render — just wrongly.
func TestReference_tableRowsHaveConsistentColumns(t *testing.T) {
	var inTable bool
	var want int
	for i, line := range strings.Split(Reference(), "\n") {
		if !strings.HasPrefix(line, "|") {
			inTable = false
			continue
		}
		n := countUnescapedPipes(line)
		if !inTable {
			inTable, want = true, n
			continue
		}
		if n != want {
			t.Errorf("line %d has %d columns, table started with %d:\n%s", i+1, n, want, line)
		}
	}
}

func countUnescapedPipes(line string) int {
	var n int
	for i := 0; i < len(line); i++ {
		if line[i] == '|' && (i == 0 || line[i-1] != '\\') {
			n++
		}
	}
	return n
}

func sectionFor(t *testing.T, ref, name string) string {
	t.Helper()
	head := "## `" + name + "`"
	i := strings.Index(ref, head)
	if i < 0 {
		t.Fatalf("no section for %s", name)
	}
	rest := ref[i+len(head):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j]
	}
	return rest
}
