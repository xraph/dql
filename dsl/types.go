package dsl

import "encoding/json"

// --- Query DSL types ---

// QueryDSL is the top-level query structure parsed from JSON.
type QueryDSL struct {
	// Mode selects the execution model. "" (default) runs the classic field-based
	// query. "pipe" treats the Pipe array as an ordered stream of operator stages
	// applied after the source dataset is read.
	Mode          string            `json:"mode,omitempty"`
	Pipe          []PipeStage       `json:"pipe,omitempty"`
	From          FromClause        `json:"from"`
	ProjectID     string            `json:"projectId,omitempty"`
	Join          []JoinClause      `json:"join,omitempty"`
	Where         *WhereClause      `json:"where,omitempty"`
	Select        []SelectField     `json:"select,omitempty"`
	Computed      []ComputedColumn  `json:"computed,omitempty"`
	GroupBy       []string          `json:"groupBy,omitempty"`
	Aggregate     []AggregateClause `json:"aggregate,omitempty"`
	Having        *WhereClause      `json:"having,omitempty"`
	OrderBy       []OrderByClause   `json:"orderBy,omitempty"`
	Limit         *int              `json:"limit,omitempty"`
	Offset        *int              `json:"offset,omitempty"`
	WithFormulas  bool              `json:"withFormulas,omitempty"`
	WithFunctions []string          `json:"withFunctions,omitempty"`
	Parameters    map[string]any    `json:"parameters,omitempty"`
	Viz           *VizOutput        `json:"viz,omitempty"`
	Expand        *ExpandConfig     `json:"expand,omitempty"`
	// OmitPipeStats opts out of the per-stage pipe telemetry on the response
	// (stats.pipe). Default false — pipe telemetry is included for every
	// pipe-mode query. Set to true on hot paths or when the client doesn't
	// need observability data, to shave bytes off the wire.
	OmitPipeStats bool `json:"omit_pipe_stats,omitempty"`
}

// IsPipeMode returns true when this query should be dispatched to the pipe executor.
func (q *QueryDSL) IsPipeMode() bool {
	return q.Mode == "pipe"
}

// QueryParameter declares a typed parameter on a saved query. The shape
// mirrors function.Parameter so authors see one consistent model across
// query and DTL function authoring. Type is one of the strings returned
// by ValidParameterType (string, int32, int64, float, bool, datetime in
// v1; arrays come later). Required parameters with no Default must be
// supplied at execute time; optional parameters fall back to Default.
type QueryParameter struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// ValidParameterType reports whether t is a recognised parameter type
// string. Kept in the domain package so both the save-time validator
// and the runtime coercion helper agree on the allowlist.
func ValidParameterType(t string) bool {
	switch t {
	case "string", "int32", "int64", "float", "bool", "datetime":
		return true
	}
	return false
}

// PipeStage is a single step in a pipe-mode query.
//
// Op identifies the operator and Config carries the raw per-op JSON decoded by
// the op factory. ID and From describe how the stage participates in the
// pipeline graph: ID names the stage's output, and From overrides the default
// "previous stage" input by referencing an earlier stage's ID. When From is
// empty the stage consumes the previous stage's output (the default linear
// chain). When ID is empty the stage's output is anonymous and cannot be
// referenced by later stages — the previous-stage chain still works.
type PipeStage struct {
	Op     string          `json:"op"`
	ID     string          `json:"id,omitempty"`
	From   string          `json:"from,omitempty"`
	Config json.RawMessage `json:"-"`
}

// UnmarshalJSON stashes the full stage object as the raw Config, then extracts
// Op, ID, and From so the executor can wire stages into the graph without a
// second parse.
func (s *PipeStage) UnmarshalJSON(data []byte) error {
	var head struct {
		Op   string `json:"op"`
		ID   string `json:"id"`
		From string `json:"from"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	s.Op = head.Op
	s.ID = head.ID
	s.From = head.From
	s.Config = append(s.Config[:0], data...)
	return nil
}

// MarshalJSON emits the raw config (which already includes `op`). When Config is
// empty — typically in tests that construct stages directly — it falls back to
// the Op-only form.
func (s PipeStage) MarshalJSON() ([]byte, error) {
	if len(s.Config) > 0 {
		return s.Config, nil
	}
	return json.Marshal(struct {
		Op string `json:"op"`
	}{Op: s.Op})
}

// ExpandConfig controls reference metadata enrichment in query results.
// When enabled, reference columns are enriched with a _meta field containing
// display information (e.g., user name/avatar instead of just an ID).
//
// Accepts:
//   - true          → expand all reference columns
//   - ["col1","col2"] → expand only named columns
type ExpandConfig struct {
	All     bool     // true = expand all reference columns
	Columns []string // specific columns to expand (when All is false)
}

func (e *ExpandConfig) UnmarshalJSON(data []byte) error {
	// Try bool first: "expand": true
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		e.All = b
		return nil
	}

	// Try string array: "expand": ["created_by", "team_id"]
	var cols []string
	if err := json.Unmarshal(data, &cols); err == nil {
		e.Columns = cols
		return nil
	}

	return nil
}

// ShouldExpand returns true if the given column should be enriched.
func (e *ExpandConfig) ShouldExpand(column string) bool {
	if e.All {
		return true
	}
	for _, c := range e.Columns {
		if c == column {
			return true
		}
	}
	return false
}

// FromClause identifies the data source for a query.
type FromClause struct {
	Dataset string `json:"dataset,omitempty"`
}

// UnmarshalJSON handles both string ("dataset_name") and object ({"dataset":"..."}) forms.
func (f *FromClause) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.Dataset = s
		return nil
	}
	// Fall back to struct
	type alias FromClause
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*f = FromClause(a)
	return nil
}

// JoinClause defines a join between two datasets.
type JoinClause struct {
	Dataset string `json:"dataset"`
	Alias   string `json:"alias,omitempty"`
	On      JoinOn `json:"on"`
	Type    string `json:"type"` // inner, left, right, full, cross
}

// JoinOn specifies join columns.
type JoinOn struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

// WhereClause is a recursive filter condition.
type WhereClause struct {
	// Simple condition
	Field string `json:"field,omitempty"`
	Op    string `json:"op,omitempty"`
	Value any    `json:"value,omitempty"`

	// DTL expression alternative
	Expr string `json:"expr,omitempty"`

	// Compound conditions
	And []WhereClause `json:"and,omitempty"`
	Or  []WhereClause `json:"or,omitempty"`
	Not *WhereClause  `json:"not,omitempty"`
}

// UnmarshalJSON handles both string ("expr") and object ({"field":"...", ...}) forms.
func (w *WhereClause) UnmarshalJSON(data []byte) error {
	// Try string first — treat as DTL expression
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		w.Expr = s
		return nil
	}
	// Fall back to struct
	type alias WhereClause
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*w = WhereClause(a)
	return nil
}

// IsSimple returns true if this is a simple field-op-value condition.
func (w *WhereClause) IsSimple() bool {
	return w.Field != "" && w.Op != ""
}

// IsExpr returns true if this uses a DTL expression.
func (w *WhereClause) IsExpr() bool {
	return w.Expr != ""
}

// IsCompound returns true if this is an AND/OR/NOT compound condition.
func (w *WhereClause) IsCompound() bool {
	return len(w.And) > 0 || len(w.Or) > 0 || w.Not != nil
}

// SelectField represents a column or expression in the SELECT clause.
type SelectField struct {
	Field string `json:"field,omitempty"` // simple column reference
	Expr  string `json:"expr,omitempty"`  // DTL expression
	As    string `json:"as,omitempty"`    // alias
}

// UnmarshalJSON handles both string ("field_name") and object ({"field":"...", "as":"..."}) forms.
func (s *SelectField) UnmarshalJSON(data []byte) error {
	// Try string first
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.Field = str
		return nil
	}
	// Fall back to struct
	type alias SelectField
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*s = SelectField(a)
	return nil
}

// OutputName returns the display name for this field in results.
func (s *SelectField) OutputName() string {
	if s.As != "" {
		return s.As
	}
	if s.Field != "" {
		return s.Field
	}
	return s.Expr
}

// ComputedColumn defines an in-memory computed column using a DTL expression.
type ComputedColumn struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
}

// AggregateClause defines an aggregation function.
type AggregateClause struct {
	Fn    string `json:"fn"`              // COUNT, SUM, AVG, MIN, MAX, STDEV, VARIANCE, MEDIAN, PERCENTILE, EXPR, etc.
	Field string `json:"field,omitempty"` // column to aggregate
	Expr  string `json:"expr,omitempty"`  // for fn=EXPR, a DTL expression
	Args  []any  `json:"args,omitempty"`  // extra args (e.g., percentile value)
	As    string `json:"as"`              // output alias
}

// IsPushable returns true if this aggregate can be pushed to SQL.
func (a *AggregateClause) IsPushable() bool {
	switch a.Fn {
	case "COUNT", "SUM", "AVG", "MIN", "MAX":
		return a.Expr == "" // only pushable if using a simple field
	default:
		return false
	}
}

// OrderByClause defines a sort order.
type OrderByClause struct {
	Field string `json:"field,omitempty"`
	Expr  string `json:"expr,omitempty"`
	Dir   string `json:"dir"` // "asc" or "desc"
}

// --- Valid operator/function enums ---

// ValidWhereOps is the set of recognized WHERE operators.
//
// starts_with / ends_with / contains are friendlier alternatives to
// `like` for the common substring/prefix/suffix cases. The value
// supplied is a literal string — the engine adds the wildcards (or
// equivalent regex anchors per driver) under the hood, so callers
// don't have to know SQL `%` conventions and can't smuggle wildcards
// they didn't mean (a `_` in a slug doesn't accidentally become a
// single-char wildcard).
var ValidWhereOps = map[string]bool{
	"==": true, "!=": true, ">": true, "<": true, ">=": true, "<=": true,
	"in": true, "not_in": true,
	"like": true, "not_like": true,
	"starts_with": true, "ends_with": true, "contains": true,
	"is_null": true, "is_not_null": true, "between": true,
}

// SQLOpMap maps DSL operators to SQL operators.
var SQLOpMap = map[string]string{
	"==": "=", "!=": "!=", ">": ">", "<": "<", ">=": ">=", "<=": "<=",
	"like": "LIKE", "not_like": "NOT LIKE",
	"is_null": "IS NULL", "is_not_null": "IS NOT NULL",
}

// ValidAggregateFns is the set of recognized aggregate function names.
var ValidAggregateFns = map[string]bool{
	"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true,
	"STDEV": true, "VARIANCE": true, "MEDIAN": true, "PERCENTILE": true,
	"FIRST": true, "LAST": true, "ARRAY_AGG": true, "STRING_AGG": true,
	"COUNTIF": true, "SUMIF": true, "EXPR": true,
}

// ValidJoinTypes is the set of recognized join types.
var ValidJoinTypes = map[string]bool{
	"inner": true, "left": true, "right": true, "full": true, "cross": true,
}

// --- Result types ---

// Row is a single result row.
type Row = map[string]any

// QueryResult holds the complete result of a query execution.
// QueryResult is the response shape for /api/v1/query/execute.
//
// The shape is intentionally tabular ("rows" + "columns") rather than the
// generic dto.ListResponse[T] envelope used by typed-entity list endpoints.
// Query results are dynamic map rows with no fixed schema, so the data is
// addressable as rows-of-cells, not items-of-T.
//
// Pagination field names (total, page, page_size, has_more) intentionally
// mirror dto.ListResponse so a thin shared reader can normalise both shapes
// when needed.
//
// JSON shape:
//
//	{
//	  "rows": [...],
//	  "columns": [...],
//	  "total": 12,
//	  "page": 1,
//	  "page_size": 100,
//	  "has_more": false,
//	  "stats": {...}
//	}
type QueryResult struct {
	Rows     []Row        `json:"rows"`
	Columns  []ColumnInfo `json:"columns"`
	Total    *int         `json:"total,omitempty"`
	Page     *int         `json:"page,omitempty"`
	PageSize *int         `json:"page_size,omitempty"`
	HasMore  bool         `json:"has_more"`
	Stats    QueryStats   `json:"stats"`
}

// NewQueryResult builds a QueryResult containing the given rows. Total is set
// from len(rows). Columns and Stats are left zero — engines populate them
// after construction. Useful for tests and quick adapters.
func NewQueryResult(rows []Row) *QueryResult {
	total := len(rows)
	return &QueryResult{Rows: rows, Total: &total}
}

// ColumnInfo describes a column in the result set.
type ColumnInfo struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Source string `json:"source"` // "raw", "computed", "aggregate"
}

// QueryStats holds execution metrics.
type QueryStats struct {
	ExecutionMs  int64    `json:"executionMs"`
	RowsScanned  int64    `json:"rowsScanned"`
	RowsReturned int      `json:"rowsReturned"`
	Sources      []string `json:"sourcesQueried"`
	// Pipe carries per-stage telemetry for pipe-mode queries: input/output
	// row counts, elapsed time, and operator-specific labels (e.g. tap's
	// label). Only populated for pipe-mode queries; absent for classic.
	Pipe []PipeStageStat `json:"pipe,omitempty"`
	// Truncated is set when an app-source fetch hit the safety cap — the
	// result may be missing rows the app never returned.
	Truncated bool `json:"truncated,omitempty"`
}

// PipeStageStat is the per-stage observability payload for a pipe-mode query.
// Pushed stages (folded into the SQL/Mongo prefix) are summarised together
// rather than individually — only in-memory tail stages get a row here.
type PipeStageStat struct {
	// Index is the position in the original q.Pipe array (0-based).
	Index int `json:"index"`
	// ID is the user-supplied stage id (empty when the stage was anonymous).
	ID string `json:"id,omitempty"`
	// Op is the operator name ("filter", "tap", "callApp", ...).
	Op string `json:"op"`
	// Label is an operator-specific debug label. Populated by `tap` from its
	// config; other ops may extend this in future.
	Label string `json:"label,omitempty"`
	// RowsIn is the number of rows the stage received as input.
	RowsIn int `json:"rowsIn"`
	// RowsOut is the number of rows the stage produced as output.
	RowsOut int `json:"rowsOut"`
	// DurationMs is the wall-clock time spent in this stage's Apply.
	DurationMs int64 `json:"durationMs"`
	// Sample is a small snapshot of the rows this stage produced. Capped at
	// PipeStageStatSampleSize so the stats payload stays bounded on large
	// pipelines. Useful for debugging — see what each stage produced
	// without adding a `tap` and rerunning. Empty when the stage produced
	// zero rows or when sampling was suppressed.
	Sample []Row `json:"sample,omitempty"`
}

// PipeStageStatSampleSize caps the number of rows captured per stage in
// PipeStageStat.Sample. Five is enough to eyeball the column shape and a
// few representative values without bloating the response.
const PipeStageStatSampleSize = 5

// --- Query Plan (for explain) ---

// QueryPlan describes the execution strategy for a query.
type QueryPlan struct {
	Dataset      *DatasetInfo      `json:"-"` // resolved schema info (not serialized)
	DatasetName  string            `json:"datasetName"`
	TableName    string            `json:"tableName"`
	PushedWhere  *WhereClause      `json:"pushedWhere,omitempty"`
	PushedSelect []string          `json:"pushedSelect,omitempty"`
	PushedOrder  []OrderByClause   `json:"pushedOrder,omitempty"`
	PushedGroup  []string          `json:"pushedGroup,omitempty"`
	PushedAggs   []AggregateClause `json:"pushedAggs,omitempty"`
	PushedLimit  *int              `json:"pushedLimit,omitempty"`
	PushedOffset *int              `json:"pushedOffset,omitempty"`
	HasHaving    bool              `json:"hasHaving,omitempty"`
	PushedHaving *WhereClause      `json:"pushedHaving,omitempty"`
	Joins        []JoinPlan        `json:"joins,omitempty"`
	InMemory     []string          `json:"inMemoryOps,omitempty"`
	SQL          string            `json:"sql,omitempty"`
	SQLParams    []any             `json:"sqlParams,omitempty"`
	Columns      []ColumnInfo      `json:"columns,omitempty"`
}

// JoinPlan describes a resolved join in the query plan.
type JoinPlan struct {
	TableName string `json:"tableName"`
	Alias     string `json:"alias"`
	OnLeft    string `json:"onLeft"`
	OnRight   string `json:"onRight"`
	Type      string `json:"type"`
	// ScopeColumns names the host's partition columns that this joined table
	// actually carries, in scope order. The SQL generator adds a predicate per
	// entry to the ON clause — not WHERE — so an out-of-scope row fails the
	// join rather than NULL-padding through a LEFT join.
	ScopeColumns []string `json:"scopeColumns,omitempty"`
}

// --- Schema resolver types (used by planner) ---

// DatasetInfo holds resolved schema metadata for a dataset.
type DatasetInfo struct {
	ID        string
	Name      string
	TableName string // PostgreSQL table name (prefixed with ds_)
	Columns   []ColumnMeta
	Versioned bool // true if dataset has per-row versioning enabled
}

// ColumnMeta describes a single column's metadata.
type ColumnMeta struct {
	Name       string
	Type       string
	Source     string // "raw", "formula", "virtual", "query"
	RefDataset string // "@user", "@team", or dataset name for reference columns
	RefColumn  string // referenced column (defaults to primary key)
}

// IsRaw returns true if this column stores actual data (not computed).
func (c *ColumnMeta) IsRaw() bool {
	return c.Source == "" || c.Source == "raw"
}

// --- Viz output types ---

// VizOutput configures visualization output for a query result.
// When present on a QueryDSL, the query result is piped through the viz
// transformer and the rendered visualization is included in the response.
type VizOutput struct {
	Type    string         `json:"type"`              // viz type: "chart", "pivot", "table", "kpi", "heatmap", "treemap", "summary", "gauge"
	Config  map[string]any `json:"config"`            // type-specific config (matches the viz extension's per-type config objects)
	AutoViz bool           `json:"autoViz,omitempty"` // when true, auto-detect the best viz type from column metadata
}
