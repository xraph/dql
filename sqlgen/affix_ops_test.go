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

func TestSQLGen_caseInsensitiveAffixOps(t *testing.T) {
	cases := []struct {
		op      string
		value   string
		wantSQL string // the rendered condition, exactly
		wantArg string
	}{
		{op: "istarts_with", value: "Acme", wantSQL: `"name" ILIKE $2 ESCAPE '\'`, wantArg: "Acme%"},
		{op: "iends_with", value: "Acme", wantSQL: `"name" ILIKE $2 ESCAPE '\'`, wantArg: "%Acme"},
		{op: "icontains", value: "acme", wantSQL: `"name" ILIKE $2 ESCAPE '\'`, wantArg: "%acme%"},
	}

	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			plan := &dsl.QueryPlan{
				DatasetName: "customers",
				TableName:   "ds_customers",
				PushedWhere: &dsl.WhereClause{Field: "name", Op: tc.op, Value: tc.value},
			}
			sql, args, err := GenerateSQL(plan, testScope("ws_test", ""))
			if err != nil {
				t.Fatalf("GenerateSQL: %v", err)
			}
			if !strings.Contains(sql, tc.wantSQL) {
				t.Errorf("SQL missing %q\nfull: %s", tc.wantSQL, sql)
			}
			if strings.Contains(sql, `"name" LIKE`) {
				t.Errorf("case-insensitive op rendered a case-sensitive LIKE\nfull: %s", sql)
			}
			if len(args) != 2 || args[1] != tc.wantArg {
				t.Errorf("args = %v, want second arg %q", args, tc.wantArg)
			}
		})
	}
}

// The case-insensitive ops take a literal value exactly like their
// case-sensitive forms: `%`, `_` and `\` in it are escaped, not wildcards.
func TestSQLGen_caseInsensitiveAffixOpsEscapeWildcards(t *testing.T) {
	cases := []struct {
		op, value, want string
	}{
		{"icontains", `10%_te\st`, `%10\%\_te\\st%`},
		{"istarts_with", `a_b`, `a\_b%`},
		{"iends_with", `50%`, `%50\%`},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			plan := &dsl.QueryPlan{
				DatasetName: "spaces",
				TableName:   "ds_spaces",
				PushedWhere: &dsl.WhereClause{Field: "name", Op: tc.op, Value: tc.value},
			}
			_, args, err := GenerateSQL(plan, testScope("ws_test", ""))
			if err != nil {
				t.Fatalf("GenerateSQL: %v", err)
			}
			if len(args) != 2 || args[1] != tc.want {
				t.Errorf("args = %v, want escaped second arg %q", args, tc.want)
			}
		})
	}
}

func TestSQLGen_icontainsInsideOr(t *testing.T) {
	plan := &dsl.QueryPlan{
		DatasetName: "customers",
		TableName:   "ds_customers",
		PushedWhere: &dsl.WhereClause{Or: []dsl.WhereClause{
			{Field: "name", Op: "icontains", Value: "acme"},
			{Field: "email", Op: "icontains", Value: "acme"},
		}},
	}
	sql, args, err := GenerateSQL(plan, testScope("ws_test", ""))
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}
	want := `("name" ILIKE $2 ESCAPE '\' OR "email" ILIKE $3 ESCAPE '\')`
	if !strings.Contains(sql, want) {
		t.Errorf("SQL missing %q\nfull: %s", want, sql)
	}
	if len(args) != 3 || args[1] != "%acme%" || args[2] != "%acme%" {
		t.Errorf("args = %v", args)
	}
}
