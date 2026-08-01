package pipe

import "testing"

func TestDatasetItems_appRefsTaggedAsAppKind(t *testing.T) {
	items := datasetItems([]string{"sensors", "system:users", "app:conn1/telemetry"}, "")
	kinds := map[string]string{}
	for _, it := range items {
		kinds[it.Label] = it.Kind
	}
	if kinds["sensors"] != "dataset" || kinds["system:users"] != "dataset" {
		t.Fatalf("plain/system refs must stay kind dataset: %v", kinds)
	}
	if kinds["app:conn1/telemetry"] != "app" {
		t.Fatalf("app ref must be kind app, got %q", kinds["app:conn1/telemetry"])
	}
}
