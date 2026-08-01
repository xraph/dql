package pipe

import (
	"encoding/json"
	"testing"
)

func TestCatalog_everyRegisteredOpHasMetadata(t *testing.T) {
	cat := CatalogIndex()
	for _, op := range RegisteredOps() {
		if _, ok := cat[op]; !ok {
			t.Fatalf("registered op %q has no catalog entry; add metadata in catalog.go", op)
		}
	}
}

func TestCatalog_marshalsToJSON(t *testing.T) {
	for _, m := range Catalog() {
		_, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("op %q config schema does not marshal: %v", m.Name, err)
		}
	}
}

func TestSchema_topLevelShape(t *testing.T) {
	s := Schema()
	if s["$schema"] != "http://json-schema.org/draft-07/schema#" {
		t.Fatalf("schema $id wrong: %v", s["$schema"])
	}
	props, _ := s["properties"].(map[string]any)
	if _, ok := props["pipe"]; !ok {
		t.Fatalf("schema must declare a `pipe` property")
	}
	defs, _ := s["$defs"].(map[string]any)
	if _, ok := defs["PipeStage"]; !ok {
		t.Fatalf("schema must declare $defs.PipeStage")
	}
}

func TestSchema_pipeStageOneOfCoversAllOps(t *testing.T) {
	s := Schema()
	defs := s["$defs"].(map[string]any)
	stage := defs["PipeStage"].(map[string]any)
	branches := stage["oneOf"].([]any)
	if len(branches) < len(Catalog()) {
		t.Fatalf("PipeStage oneOf has %d branches; want at least %d", len(branches), len(Catalog()))
	}
}

func TestSchema_pipeStageBranchHasOpDiscriminator(t *testing.T) {
	s := Schema()
	defs := s["$defs"].(map[string]any)
	stage := defs["PipeStage"].(map[string]any)
	branches := stage["oneOf"].([]any)
	for _, b := range branches {
		branch := b.(map[string]any)
		props := branch["properties"].(map[string]any)
		op, ok := props["op"].(map[string]any)
		if !ok {
			t.Fatalf("branch missing op discriminator: %+v", branch)
		}
		if _, ok := op["const"]; !ok {
			t.Fatalf("op discriminator must use const, got %+v", op)
		}
	}
}
