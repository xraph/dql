package pipe

import (
	"os"
	"regexp"
	"testing"
)

// The README's pipe example named an operator that does not exist (`sortLimit`)
// and went unnoticed, because nothing checked it. A reader copying the flagship
// example got "unknown pipe op" as their first experience of the language.
//
// This does not validate operator configs — that would need a YAML parser, and
// this module deliberately has no dependencies. It does catch the failure that
// actually happened: documenting an operator that was never registered.
var docStageOp = regexp.MustCompile(`(?m)^\s*-\s*op:\s*"?([A-Za-z]\w*)"?`)

func TestDocs_readmeNamesOnlyRealOperators(t *testing.T) {
	const path = "../README.md"
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	matches := docStageOp.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatalf("%s has no `- op:` pipe stages — has the example moved? "+
			"This test is worthless if it matches nothing.", path)
	}
	for _, m := range matches {
		if !Known(m[1]) {
			t.Errorf("%s documents `op: %s`, which is not a registered operator", path, m[1])
		}
	}
}
