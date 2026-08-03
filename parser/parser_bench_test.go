package parser

import (
	"encoding/json"
	"testing"
)

var benchSimple = json.RawMessage(`{
  "from": {"dataset": "spaces"},
  "where": {"field": "parent_id", "op": "==", "value": "x"},
  "orderBy": [{"field": "sort_order", "dir": "asc"}]
}`)

var benchPipe = json.RawMessage(`{
  "mode": "pipe",
  "from": {"dataset": "events"},
  "pipe": [
    {"op": "filter", "where": {"field": "status", "op": "==", "value": "open"}},
    {"op": "groupBy", "keys": ["assignee"]},
    {"op": "aggregate", "aggs": [{"fn": "count", "as": "total"}]},
    {"op": "sort", "by": [{"field": "total", "dir": "desc"}]},
    {"op": "limit", "n": 10}
  ]
}`)

var benchDocs = []struct {
	name string
	doc  json.RawMessage
}{
	{"simple", benchSimple},
	{"pipe", benchPipe},
}

func BenchmarkParse(b *testing.B) {
	for _, tc := range benchDocs {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				q, errs := Parse(tc.doc)
				if len(errs) > 0 || q == nil {
					b.Fatalf("parse: %v", errs)
				}
			}
		})
	}
}

func BenchmarkValidate(b *testing.B) {
	for _, tc := range benchDocs {
		q, errs := Parse(tc.doc)
		if len(errs) > 0 {
			b.Fatalf("setup parse: %v", errs)
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if e := Validate(q); len(e) > 0 {
					b.Fatalf("validate: %v", e)
				}
			}
		})
	}
}
