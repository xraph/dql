// Package dql_test holds the module's boundary guard.
//
// This library is meant to be embeddable by any host. A dependency on one
// particular host would silently undo that without necessarily breaking a
// build — the code would still compile here, where that host is present.
package dql_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Only these modules in the namespace may be depended on: this one, and the
// expression language the processor evaluates through.
var allowed = []string{"github.com/xraph/dql", "github.com/xraph/dtl"}

// The rule is stated as an exclusion rather than a list of banned hosts, so it
// keeps covering whichever sibling module is written next.
const siblingNamespace = "github.com/xraph/"

func TestModule_hasNoHostDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	for _, dep := range strings.Split(string(out), "\n") {
		dep = strings.TrimSpace(dep)
		if !strings.HasPrefix(dep, siblingNamespace) {
			continue
		}
		var ok bool
		for _, a := range allowed {
			if dep == a || strings.HasPrefix(dep, a+"/") {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("dql must not depend on %s — the query engine stays host-agnostic", dep)
		}
	}
}
