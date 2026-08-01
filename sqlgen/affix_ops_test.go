package sqlgen

import (
	"strings"
	"testing"

	"github.com/xraph/dql/dsl"
)

func TestSQLGen_affixOps(t *testing.T) {
	cases := []struct {
		op           string
		value        string
		wantContains []string // substrings that must appear in the SQL
		wantArg      string   // placeholder argument
	}{
		{
			op:           "starts_with",
			value:        "/team/alpha",
			wantContains: []string{`"path" LIKE`, `ESCAPE '\'`},
			wantArg:      "/team/alpha%",
		},
		{
			op:           "ends_with",
			value:        "/foo",
			wantContains: []string{`"path" LIKE`, `ESCAPE '\'`},
			wantArg:      "%/foo",
		},
		{
			op:           "contains",
			value:        "alpha",
			wantContains: []string{`"path" LIKE`, `ESCAPE '\'`},
			wantArg:      "%alpha%",
		},
	}

	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			plan := &dsl.QueryPlan{
				DatasetName: "spaces",
				TableName:   "ds_spaces",
				PushedWhere: &dsl.WhereClause{
					Field: "path", Op: tc.op, Value: tc.value,
				},
			}
			sql, args, err := GenerateSQL(plan, testScope("ws_test", ""))
			if err != nil {
				t.Fatalf("GenerateSQL: %v", err)
			}
			for _, sub := range tc.wantContains {
				if !strings.Contains(sql, sub) {
					t.Errorf("SQL missing %q\nfull: %s", sub, sql)
				}
			}
			found := false
			for _, a := range args {
				if a == tc.wantArg {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected arg %q in %v", tc.wantArg, args)
			}
		})
	}
}

func TestSQLGen_affixOpsEscapeWildcards(t *testing.T) {
	plan := &dsl.QueryPlan{
		DatasetName: "spaces",
		TableName:   "ds_spaces",
		PushedWhere: &dsl.WhereClause{
			Field: "name", Op: "contains", Value: "10%_test",
		},
	}
	_, args, err := GenerateSQL(plan, testScope("ws_test", ""))
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}
	want := `%10\%\_test%`
	found := false
	for _, a := range args {
		if a == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected escaped arg %q in %v", want, args)
	}
}
