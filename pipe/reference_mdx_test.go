package pipe

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var websiteDocs = flag.String("update-website-docs", "",
	"write the MDX operator reference to this path")

const (
	websiteTitle = "Operators"
	websiteDesc  = "Every operator in the DQL pipe catalog, with its configuration, " +
		"whether it is live-safe, whether it can push into SQL, and the host services it needs."
)

// TestReferenceMDX_write emits the site's operator page when -update-website-docs
// names a destination, and does nothing otherwise. It mirrors how
// docs/OPERATORS.md is produced, so both come from one catalog.
//
// Unlike OPERATORS.md there is no staleness test for the site copy: it lives in
// another repository, which this module's CI cannot see. Regenerating is a step
// in `make docs-website`, and the page says so in a comment banner.
func TestReferenceMDX_write(t *testing.T) {
	if *websiteDocs == "" {
		t.Skip("no -update-website-docs path given")
	}
	dir := filepath.Dir(*websiteDocs)
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("destination %s is not present, skipping: %v", dir, err)
	}
	body := ReferenceMDX(websiteTitle, websiteDesc)
	if err := os.WriteFile(*websiteDocs, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", *websiteDocs, err)
	}
	t.Logf("wrote %s (%d bytes, %d operators)", *websiteDocs, len(body), len(Catalog()))
}

// An unescaped `{` or `<` outside a code fence is a build failure on the
// consuming MDX site, not a rendering blemish — the page simply does not
// compile. The catalog contains both in prose today (a conditional requirement
// renders as `conditional {"kind":["expr"]}`), so this is a live hazard rather
// than a theoretical one.
func TestReferenceMDX_hasNoUnescapedMDXSyntax(t *testing.T) {
	body := ReferenceMDX("Operators", "desc")
	inFence := false
	var offenders []string
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// The generated comment banner is a deliberate MDX expression.
		if strings.HasPrefix(trimmed, "{/*") || strings.HasPrefix(trimmed, "run `make") ||
			strings.HasPrefix(trimmed, "Do not edit") {
			continue
		}
		parts := strings.Split(line, "`")
		for j, p := range parts {
			if j%2 == 1 { // inline code — MDX leaves it alone
				continue
			}
			for _, bad := range []string{"{", "<"} {
				idx := strings.Index(p, bad)
				for idx >= 0 {
					if idx == 0 || p[idx-1] != '\\' {
						offenders = append(offenders,
							strings.TrimSpace(line)+"  (line "+itoa(i+1)+")")
						idx = -1
						continue
					}
					next := strings.Index(p[idx+1:], bad)
					if next < 0 {
						idx = -1
					} else {
						idx = idx + 1 + next
					}
				}
			}
		}
	}
	if len(offenders) > 0 {
		t.Errorf("%d line(s) carry unescaped MDX syntax and would break the site build:", len(offenders))
		for _, o := range offenders {
			if len(o) > 120 {
				o = o[:120] + "…"
			}
			t.Errorf("  %s", o)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// The escaping must not reach inside code fences, where braces are legitimate
// JSON and MDX already ignores them.
func TestEscapeMDX_leavesCodeFencesAlone(t *testing.T) {
	in := "text {a}\n```json\n{\"k\": [1]}\n```\nmore {b}\n"
	got := escapeMDX(in)
	if !strings.Contains(got, "{\"k\": [1]}") {
		t.Errorf("fenced JSON was escaped:\n%s", got)
	}
	if !strings.Contains(got, "text \\{a}") || !strings.Contains(got, "more \\{b}") {
		t.Errorf("prose braces were not escaped:\n%s", got)
	}
}

func TestEscapeMDX_leavesInlineCodeAlone(t *testing.T) {
	got := escapeMDX("prose {x} and `code {y}` end")
	if !strings.Contains(got, "`code {y}`") {
		t.Errorf("inline code was escaped: %s", got)
	}
	if !strings.Contains(got, "prose \\{x}") {
		t.Errorf("prose brace not escaped: %s", got)
	}
}

// The MDX page must describe exactly the operators that are registered.
func TestReferenceMDX_coversEveryOperator(t *testing.T) {
	mdx := ReferenceMDX("Operators", "desc")
	for _, m := range Catalog() {
		if !strings.Contains(mdx, "## `"+m.Name+"`") {
			t.Errorf("%s has no section in the MDX reference", m.Name)
		}
	}
}
