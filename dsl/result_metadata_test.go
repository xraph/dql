package dsl_test

import (
	"encoding/json"
	"github.com/xraph/dql/dsl"
	"testing"
)

func TestQueryResultPreservesMetadataWithNoRows(t *testing.T) {
	wire := []byte(`{"rows":[],"columns":[],"has_more":false,"metadata":{"calendar":{"complete":false,"snapshot_id":"snapshot","sources":[{"binding_id":"roster","state":"unavailable"}]}}}`)
	var result dsl.QueryResult
	if err := json.Unmarshal(wire, &result); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 0 {
		t.Fatalf("metadata added synthetic rows: %v", result.Rows)
	}
	if _, ok := roundTrip["metadata"]; !ok {
		t.Fatal("source-completeness metadata was discarded from an empty result")
	}
	var metadata struct {
		Calendar struct {
			Complete bool   `json:"complete"`
			Snapshot string `json:"snapshot_id"`
			Sources  []struct {
				State string `json:"state"`
			} `json:"sources"`
		} `json:"calendar"`
	}
	if err = json.Unmarshal(roundTrip["metadata"], &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Calendar.Complete || metadata.Calendar.Snapshot != "snapshot" || len(metadata.Calendar.Sources) != 1 || metadata.Calendar.Sources[0].State != "unavailable" {
		t.Fatalf("changed metadata: %s", roundTrip["metadata"])
	}
}
func TestOrdinaryQueryResultOmitsMetadata(t *testing.T) {
	result := dsl.NewQueryResult([]dsl.Row{{"title": "Work"}})
	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["metadata"]; ok {
		t.Fatal("ordinary queries gained a metadata field")
	}
}
