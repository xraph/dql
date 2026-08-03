package pipe

// Vendor extensions used by ConfigSchema fields below.
//
// JSON Schema is generic: it tells a frontend that a field is a `string`
// but not whether the string should be a column-picker, a duration input,
// a function name autocomplete, etc. We layer three vendor-specific
// extensions on top of the standard schema so a form-builder can render
// the right control without hardcoding per-op knowledge.
//
//	x-dql-input         (string)
//	  Semantic kind of the field. Frontends read this to choose the input
//	  widget. See InputKind* constants below for the closed set of values.
//
//	x-dql-property-order ([]string)
//	  Ordered list of property names defining how a form should lay out
//	  the fields. JSON object property order isn't preserved across
//	  libraries, so this is the explicit hint. Properties not listed
//	  fall through in alphabetical order.
//
//	x-dql-required-when  (object: { fieldName: [allowed-values] })
//	  The field carrying this extension is required when sibling fields
//	  match the listed values. Example: fillNulls.partitionBy is required
//	  when method ∈ {lastValue, nextValue}. Lighter-weight than
//	  JSON Schema's if/then/else for the partial-rule cases we have.
//
// All extensions use the `x-dql-` namespace so they don't collide with
// future JSON Schema vocabulary or other vendor conventions.
const (
	InputKindColumn          = "column"            // column on the upstream row stream
	InputKindColumnOutput    = "column-output"     // new/output column name (free input, no autocomplete)
	InputKindDataset         = "dataset"           // dataset identifier (referenced for joins/lookups)
	InputKindDatasetColumn   = "dataset-column"    // column on a referenced dataset (right-side of joins)
	InputKindDTLExpression   = "dtl-expression"    // DTL expression string
	InputKindFormula         = "formula"           // Excel-style formula string
	InputKindFunctionName    = "function-name"     // registered DTL function id
	InputKindAppID           = "app-id"            // managed app id
	InputKindStageID         = "stage-id"          // reference to another stage's id
	InputKindDuration        = "duration"          // duration string ("5m", "1h", ...)
	InputKindTimestamp       = "timestamp"         // RFC3339 timestamp
	InputKindTimezone        = "timezone"          // IANA timezone (e.g. "America/New_York")
	InputKindPrefix          = "prefix"            // short string used as a column-name prefix
	InputKindColumnRenameMap = "column-rename-map" // dict of column → new column name
)

// OpMetadata describes a pipe operator for language-server / UI consumption.
//
// The catalog is the authoritative source for: the JSON Schema endpoint
// (each op's ConfigSchema becomes a discriminated branch of the pipe
// schema), the completions endpoint (StageHints feed stage-keyword
// completions), and the docs page (Description + Examples).
type OpMetadata struct {
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	// LiveSafeDefault indicates the operator is pure with default config.
	// Some ops (callFunction) become live-safe only with explicit opts.
	LiveSafeDefault bool `json:"liveSafeDefault"`
	// Pushable hints whether the op can fold into the SQL prefix. Mostly
	// informational — the planner makes the final call based on field types.
	Pushable bool `json:"pushable"`
	// ConfigSchema is the JSON Schema (Draft-07) for the op's config object.
	// The "op" property is added by the caller building the union schema.
	ConfigSchema map[string]any `json:"configSchema"`
	// Examples are short JSON snippets a UI can offer as starter values.
	Examples []OpExample `json:"examples,omitempty"`
	// Requires names the host services this operator needs in its OpContext.
	// Empty means the operator is self-contained and works anywhere.
	//
	// Declared rather than discovered: without it a host cannot answer "which
	// operators work in my configuration?" except by running a query and
	// reading the error, and a completion list offers stages that are
	// guaranteed to fail. See MissingRequirements.
	Requires []Requirement `json:"requires,omitempty"`
}

// Requirement names a host service an operator depends on.
type Requirement string

const (
	// ReqEval is an expression evaluator, for the expr forms of filter,
	// compute, branch, transform and assert.
	ReqEval Requirement = "eval"
	// ReqFunctionRegistry resolves and runs named host functions.
	ReqFunctionRegistry Requirement = "functionRegistry"
	// ReqAppCaller invokes a host application endpoint.
	ReqAppCaller Requirement = "appCaller"
	// ReqFormula computes host-defined formula columns.
	ReqFormula Requirement = "formula"
	// ReqClassic runs a nested non-pipe query, which set operations, lookups
	// and as-of joins need in order to read their right-hand side.
	ReqClassic Requirement = "classic"
	// ReqAlgorithms resolves named algorithms for the algo operator.
	ReqAlgorithms Requirement = "algorithms"
)

// OpExample is one starter value for an op.
type OpExample struct {
	Title  string         `json:"title"`
	Config map[string]any `json:"config"`
}

// Catalog returns metadata for every registered op, in stable order.
//
// The order matches the conceptual flow of a pipe (source → filter →
// transform → aggregate → sort → side-effect) so completion menus surface
// the most common stages first.
func Catalog() []OpMetadata {
	return []OpMetadata{
		// Filter / projection
		filterMeta,
		projectMeta,
		renameMeta,
		dropMeta,
		// Computation / quality
		computeMeta,
		transformMeta,
		castMeta,
		dropNullsMeta,
		fillNullsMeta,
		// Reshape
		flattenMeta,
		unnestObjectMeta,
		nestMeta,
		pivotMeta,
		unpivotMeta,
		// Filtering / dedup
		distinctMeta,
		dedupeMeta,
		sampleMeta,
		assertMeta,
		// Time-series
		timeBucketMeta,
		gapfillMeta,
		// Ordering / paging
		sortMeta,
		limitMeta,
		skipMeta,
		topPerGroupMeta,
		// Aggregation
		groupByMeta,
		aggregateMeta,
		histogramMeta,
		windowMeta,
		// Joins
		lookupMeta,
		asofJoinMeta,
		crossJoinMeta,
		// Set operations
		intersectMeta,
		exceptMeta,
		// Side effects / control flow
		callFunctionMeta,
		callAppMeta,
		algoMeta,
		branchMeta,
		mergeMeta,
		tapMeta,
	}
}

// CatalogIndex returns a name → metadata map for fast lookup.
func CatalogIndex() map[string]OpMetadata {
	cat := Catalog()
	out := make(map[string]OpMetadata, len(cat))
	for _, m := range cat {
		out[m.Name] = m
	}
	return out
}

// --- Operator metadata (kept next to the catalog for easy diffing) ---

var filterMeta = OpMetadata{
	Name:            "filter",
	Requires:        []Requirement{ReqEval},
	Summary:         "Keep rows that match a predicate",
	Description:     "Filters rows by a WHERE clause. Plain field/op/value forms push down to SQL; DTL `expr` forms run in-memory.",
	LiveSafeDefault: true,
	Pushable:        true,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"where"},
		"properties": map[string]any{
			"where": withTitleDesc(
				refSchema("WhereClause"),
				"Predicate",
				"Conditions a row must satisfy to be kept. Use simple `{field, op, value}` shape for SQL-pushable filters, or `{expr: \"…\"}` for DTL expressions evaluated in-memory.",
			),
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"where"},
	},
	Examples: []OpExample{
		{Title: "Plain comparison", Config: map[string]any{
			"op": "filter", "where": map[string]any{"field": "level", "op": "==", "value": "ERROR"},
		}},
		{Title: "DTL expression", Config: map[string]any{
			"op": "filter", "where": map[string]any{"expr": "ts > now() - duration(\"1h\")"},
		}},
	},
}

var projectMeta = OpMetadata{
	Name:            "project",
	Summary:         "Select / alias columns",
	Description:     "Reduces each row to a chosen subset of columns, optionally renaming them.",
	LiveSafeDefault: true,
	Pushable:        true,
	ConfigSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"select": map[string]any{
				"title":       "Selected columns",
				"description": "Columns to keep, in order. Each entry is `{field, as?}` — set `as` to rename.",
				"type":        "array",
				"items":       refSchema("SelectField"),
			},
			"drop": map[string]any{
				"title":       "Columns to drop",
				"description": "Alternative to `select` — instead of listing what to keep, list what to remove. Mutually exclusive with `select`.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"select", "drop"},
	},
	Examples: []OpExample{
		{Title: "Pick fields", Config: map[string]any{
			"op": "project", "select": []map[string]any{{"field": "id"}, {"field": "name"}},
		}},
	},
}

var renameMeta = OpMetadata{
	Name:            "rename",
	Summary:         "Rename columns",
	Description:     "Rewrites column keys in-place. The map is `from -> to`.",
	LiveSafeDefault: true,
	Pushable:        true,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"map"},
		"properties": map[string]any{
			"map": map[string]any{
				"title":                "Rename map",
				"description":          "Object whose keys are the existing column names and whose values are the new names. Pairs not listed are left untouched.",
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
				"x-dql-input":          InputKindColumnRenameMap,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"map"},
	},
	Examples: []OpExample{
		{Title: "id → key", Config: map[string]any{"op": "rename", "map": map[string]string{"id": "key"}}},
	},
}

var dropMeta = OpMetadata{
	Name:            "drop",
	Summary:         "Remove columns",
	Description:     "Deletes the named columns from each row.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"columns"},
		"properties": map[string]any{
			"columns": map[string]any{
				"title":       "Columns to drop",
				"description": "Names of columns to remove from every row.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
				"minItems":    1,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"columns"},
	},
	Examples: []OpExample{
		{Title: "Drop secrets", Config: map[string]any{"op": "drop", "columns": []string{"password", "secret"}}},
	},
}

var computeMeta = OpMetadata{
	Name:            "compute",
	Requires:        []Requirement{ReqEval, ReqFormula},
	Summary:         "Add a computed column",
	Description:     "Evaluates a DTL expression (`kind:\"expr\"`) or Excel-style formula (`kind:\"formula\"`) per row and stores the result under `as`.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"as"},
		"properties": map[string]any{
			"as": map[string]any{
				"title":       "Output column name",
				"description": "Column to write the computed value into. Overwrites any existing column with the same name.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"expr": map[string]any{
				"title":               "DTL expression",
				"description":         "DTL expression evaluated against each row. Used when `kind` is `expr`.",
				"type":                "string",
				"x-dql-input":         InputKindDTLExpression,
				"x-dql-required-when": map[string]any{"kind": []string{"expr", ""}},
			},
			"formula": map[string]any{
				"title":               "Excel-style formula",
				"description":         "Spreadsheet formula evaluated against each row. Used when `kind` is `formula`.",
				"type":                "string",
				"x-dql-input":         InputKindFormula,
				"x-dql-required-when": map[string]any{"kind": []string{"formula"}},
			},
			"kind": map[string]any{
				"title":       "Expression kind",
				"description": "Which language `expr` / `formula` is in. Defaults to DTL `expr`.",
				"type":        "string",
				"enum":        []string{"expr", "formula"},
				"default":     "expr",
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"as", "kind", "expr", "formula"},
	},
	Examples: []OpExample{
		{Title: "DTL expr", Config: map[string]any{"op": "compute", "as": "doubled", "expr": "value * 2"}},
		{Title: "Formula", Config: map[string]any{"op": "compute", "as": "tax", "kind": "formula", "formula": "price * 0.1"}},
	},
}

var flattenMeta = OpMetadata{
	Name:            "flatten",
	Summary:         "Unnest an array column",
	Description:     "Emits one row per array element. When `as` is set, the element is stored under that key and the original field is preserved.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"field"},
		"properties": map[string]any{
			"field": map[string]any{
				"title":       "Array column",
				"description": "Column whose array values are exploded into separate rows.",
				"type":        "string",
				"x-dql-input": InputKindColumn,
			},
			"as": map[string]any{
				"title":       "Element column",
				"description": "Optional. Column name to store each element under. When unset, the element replaces the array column on each row.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"indexAs": map[string]any{
				"title":       "Index column",
				"description": "Optional. Column name to store the element's 0-based index in the source array.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"preserveEmpty": map[string]any{
				"title":       "Keep rows with empty arrays",
				"description": "When true, rows whose array is empty or null still produce one output row (with the element column nil). Default drops them.",
				"type":        "boolean",
				"default":     false,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"field", "as", "indexAs", "preserveEmpty"},
	},
	Examples: []OpExample{
		{Title: "Unnest tags", Config: map[string]any{"op": "flatten", "field": "tags"}},
	},
}

var distinctMeta = OpMetadata{
	Name:            "distinct",
	Summary:         "Deduplicate rows",
	Description:     "When `by` is empty, uniqueness uses every column; otherwise only the named keys.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"by": map[string]any{
				"title":       "Identity columns",
				"description": "Columns whose combined values define a row's identity. Leave empty to deduplicate using every column.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"by"},
	},
}

var sortMeta = OpMetadata{
	Name:            "sort",
	Summary:         "Order rows",
	Description:     "Sorts by one or more keys. Direction defaults to `asc`.",
	LiveSafeDefault: true,
	Pushable:        true,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"by"},
		"properties": map[string]any{
			"by": map[string]any{
				"title":       "Sort keys",
				"description": "Ordered list of `{field, dir}` keys. Earlier entries are the primary sort; later entries break ties.",
				"type":        "array",
				"items":       refSchema("OrderByClause"),
				"minItems":    1,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"by"},
	},
	Examples: []OpExample{
		{Title: "Top 10 by ts", Config: map[string]any{
			"op": "sort", "by": []map[string]any{{"field": "ts", "dir": "desc"}},
		}},
	},
}

var limitMeta = OpMetadata{
	Name:            "limit",
	Summary:         "Cap row count",
	LiveSafeDefault: true,
	Pushable:        true,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"n"},
		"properties": map[string]any{
			"n": map[string]any{
				"title":       "Maximum rows",
				"description": "Keep at most this many rows. Set to 0 to drop everything.",
				"type":        "integer",
				"minimum":     0,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"n"},
	},
}

var skipMeta = OpMetadata{
	Name:            "skip",
	Summary:         "Drop the first N rows",
	LiveSafeDefault: true,
	Pushable:        true,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"n"},
		"properties": map[string]any{
			"n": map[string]any{
				"title":       "Rows to skip",
				"description": "Number of rows to discard from the start of the input. Combined with limit gives offset/page semantics.",
				"type":        "integer",
				"minimum":     0,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"n"},
	},
}

var groupByMeta = OpMetadata{
	Name:            "groupBy",
	Summary:         "Set keys for the next aggregate",
	Description:     "On its own this is a pass-through. The planner pairs it with the immediately-following `aggregate` op.",
	LiveSafeDefault: true,
	Pushable:        true,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"keys"},
		"properties": map[string]any{
			"keys": map[string]any{
				"title":       "Group-by keys",
				"description": "Columns whose unique combinations define each group. The downstream aggregate folds rows within each combination.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
				"minItems":    1,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"keys"},
	},
}

var aggregateMeta = OpMetadata{
	Name:            "aggregate",
	Summary:         "Fold rows into groups",
	Description:     "Computes COUNT, SUM, AVG, MIN, MAX over groups defined by a preceding `groupBy` (or the whole stream when standalone).",
	LiveSafeDefault: true,
	Pushable:        true,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"aggs"},
		"properties": map[string]any{
			"keys": map[string]any{
				"title":       "Group keys",
				"description": "Optional. Same shape as `groupBy.keys` — when set, the aggregate uses these keys directly without a preceding `groupBy` stage.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
			"aggs": map[string]any{
				"title":       "Aggregations",
				"description": "Ordered list of `{fn, field, as}` clauses (e.g. `{fn: \"SUM\", field: \"amount\", as: \"total\"}`).",
				"type":        "array",
				"items":       refSchema("AggregateClause"),
				"minItems":    1,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"keys", "aggs"},
	},
}

var windowMeta = OpMetadata{
	Name:            "window",
	Summary:         "Per-row computation within a partition",
	Description:     "row_number, rank, dense_rank, lag, lead, first_value, last_value. Output preserves input row order.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"fn", "as"},
		"properties": map[string]any{
			"fn": map[string]any{
				"title":       "Window function",
				"description": "Which windowing function to compute per row.",
				"type":        "string",
				"enum":        []string{"row_number", "rank", "dense_rank", "lag", "lead", "first_value", "last_value"},
			},
			"partitionBy": map[string]any{
				"title":       "Partition keys",
				"description": "Columns that split the input into independent windows. Within each partition, the function is computed in `orderBy` order.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
			"orderBy": map[string]any{
				"title":       "Order within partition",
				"description": "Sort keys applied within each partition before the function runs. Required for ranking/lag/lead semantics.",
				"type":        "array",
				"items":       refSchema("OrderByClause"),
			},
			"field": map[string]any{
				"title":               "Source column",
				"description":         "Column the window function reads. Required for `lag`/`lead`/`first_value`/`last_value`; ignored for ranking functions.",
				"type":                "string",
				"x-dql-input":         InputKindColumn,
				"x-dql-required-when": map[string]any{"fn": []string{"lag", "lead", "first_value", "last_value"}},
			},
			"offset": map[string]any{
				"title":       "Offset (lag/lead)",
				"description": "Row offset for `lag`/`lead`. Defaults to 1 row.",
				"type":        "integer",
				"minimum":     0,
			},
			"as": map[string]any{
				"title":       "Output column",
				"description": "Column to write the window value into.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"default": map[string]any{
				"title":       "Default value",
				"description": "Value to emit when the window function falls outside the partition (e.g. `lag(1)` on the first row).",
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"fn", "partitionBy", "orderBy", "field", "offset", "default", "as"},
	},
}

var lookupMeta = OpMetadata{
	Name:            "lookup",
	Requires:        []Requirement{ReqClassic},
	Summary:         "Hash-join with another dataset",
	Description:     "Fetches the right side via a secondary classic query. Use `cacheTtlMs` to avoid re-fetching on every live update.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"dataset", "on"},
		"properties": map[string]any{
			"dataset": map[string]any{
				"title":       "Right-side dataset",
				"description": "Dataset name to fetch the join's right side from.",
				"type":        "string",
				"x-dql-input": InputKindDataset,
			},
			"on": map[string]any{
				"title":       "Join keys",
				"description": "Column names on each side that must equal for a match.",
				"type":        "object",
				"required":    []string{"left", "right"},
				"properties": map[string]any{
					"left":  map[string]any{"title": "Left column", "description": "Column on the streaming (left) side.", "type": "string", "x-dql-input": InputKindColumn},
					"right": map[string]any{"title": "Right column", "description": "Column on the looked-up (right) side.", "type": "string", "x-dql-input": InputKindDatasetColumn},
				},
				"x-dql-property-order": []string{"left", "right"},
			},
			"as": map[string]any{
				"title":       "Output column",
				"description": "Optional. When set, the matched right-side row is nested under this column instead of being flattened into the left row.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"select": map[string]any{
				"title":       "Right columns to keep",
				"description": "Optional. Subset of right-side columns to merge in. Empty means all columns.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindDatasetColumn},
			},
			"mode": map[string]any{
				"title":       "Join mode",
				"description": "`left` keeps every left row; `inner` drops left rows with no match.",
				"type":        "string",
				"enum":        []string{"left", "inner"},
				"default":     "left",
			},
			"where": withTitleDesc(
				refSchema("WhereClause"),
				"Right-side filter",
				"Optional WHERE clause applied to the right-side dataset before the join.",
			),
			"limit": map[string]any{
				"title":       "Right-side row cap",
				"description": "Optional. Cap the number of right-side rows fetched (useful when `dataset` is large).",
				"type":        "integer",
				"minimum":     0,
			},
			"cacheTtlMs": map[string]any{
				"title":       "Cache TTL (ms)",
				"description": "Cache the right-side fetch for this many milliseconds. 0 disables caching. Useful when the same lookup runs many times under live mode.",
				"type":        "integer",
				"minimum":     0,
				"default":     0,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"dataset", "on", "mode", "as", "select", "where", "limit", "cacheTtlMs"},
	},
}

var callFunctionMeta = OpMetadata{
	Name:            "callFunction",
	Requires:        []Requirement{ReqFunctionRegistry},
	Summary:         "Invoke a registered DTL function",
	Description:     "Calls a function from the function extension. `pure: true` declares the function side-effect-free and makes it live-safe.",
	LiveSafeDefault: false,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"name"},
		"properties": map[string]any{
			"name": map[string]any{
				"title":       "Function name",
				"description": "Fully qualified DTL function name (e.g. `geo::lookup`).",
				"type":        "string",
				"x-dql-input": InputKindFunctionName,
			},
			"mode": map[string]any{
				"title":       "Invocation mode",
				"description": "`perRow` calls the function once per row; `batch` passes the whole row stream to the function once.",
				"type":        "string",
				"enum":        []string{"perRow", "batch"},
				"default":     "perRow",
			},
			"pure": map[string]any{
				"title":       "Pure (side-effect-free)",
				"description": "When true, asserts the function has no side effects so live mode can re-run it on each update.",
				"type":        "boolean",
				"default":     false,
			},
			"args": map[string]any{
				"title":                "Arguments",
				"description":          "Object passed to the function. Values may reference row columns via `$columnName`.",
				"type":                 "object",
				"additionalProperties": map[string]any{},
			},
			"as": map[string]any{
				"title":       "Output column",
				"description": "Column to store the function result under.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"literalArgs": map[string]any{
				"title":       "Literal arguments",
				"description": "When true, `$column`-style refs in `args` are passed verbatim instead of resolved against the row.",
				"type":        "boolean",
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"name", "mode", "pure", "args", "literalArgs", "as"},
	},
	Examples: []OpExample{
		{Title: "Per-row pure", Config: map[string]any{
			"op": "callFunction", "name": "math::abs", "pure": true, "args": map[string]any{"x": "$value"},
		}},
	},
}

var callAppMeta = OpMetadata{
	Name:            "callApp",
	Requires:        []Requirement{ReqAppCaller},
	Summary:         "Invoke a managed app",
	Description:     "Calls an external app via the runtime extension. Always non-live-safe; live subscriptions require `dryRun: true`.",
	LiveSafeDefault: false,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"appId"},
		"properties": map[string]any{
			"appId": map[string]any{
				"title":       "App ID",
				"description": "Identifier of the managed app to invoke.",
				"type":        "string",
				"x-dql-input": InputKindAppID,
			},
			"method": map[string]any{
				"title":       "Method",
				"description": "App method to call. Defaults to `transform`.",
				"type":        "string",
				"default":     "transform",
			},
			"capability": map[string]any{
				"title":       "Capability",
				"description": "Capability namespace the app must expose. Defaults to `pipe_query`.",
				"type":        "string",
				"default":     "pipe_query",
			},
			"batch": map[string]any{
				"title":       "Batch mode",
				"description": "When true, the entire row stream is sent to the app once. When false, the app is invoked per row.",
				"type":        "boolean",
				"default":     true,
			},
			"payload": map[string]any{
				"title":                "Static payload",
				"description":          "Object merged into the app invocation payload alongside the rows.",
				"type":                 "object",
				"additionalProperties": map[string]any{},
			},
			"dataset": map[string]any{
				"title":       "Dataset hint",
				"description": "Optional dataset name passed to the app for context.",
				"type":        "string",
				"x-dql-input": InputKindDataset,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"appId", "method", "capability", "batch", "payload", "dataset"},
	},
}

var algoMeta = OpMetadata{
	Name:            "algo",
	Requires:        []Requirement{ReqAlgorithms},
	Summary:         "Run a registered algorithm",
	Description:     "Invokes a native or external algorithm from the shared catalog (e.g. `minmax_scale`, `kmeans`, `robust_zscore`). `params` are forwarded verbatim to the algorithm — see its descriptor for accepted keys. Live-safety depends on the chosen algorithm: pure ones (most ETL transforms) are live-safe; external/stateful ones are not.",
	LiveSafeDefault: false,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"name"},
		"properties": map[string]any{
			"name": map[string]any{
				"title":       "Algorithm name",
				"description": "Identifier of a registered algorithm (e.g. `minmax_scale`, `kmeans`). Must exist in the algorithm catalog or the stage fails at build time.",
				"type":        "string",
			},
			"params": map[string]any{
				"title":                "Algorithm parameters",
				"description":          "Algorithm-specific options passed through unchanged. Each algorithm documents its own keys in its catalog descriptor (e.g. `minmax_scale` takes `column` and `as`).",
				"type":                 "object",
				"additionalProperties": map[string]any{},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"name", "params"},
	},
	Examples: []OpExample{
		{Title: "Min-max scale a column", Config: map[string]any{
			"op": "algo", "name": "minmax_scale", "params": map[string]any{"column": "v", "as": "v_scaled"},
		}},
	},
}

var branchMeta = OpMetadata{
	Name:            "branch",
	Requires:        []Requirement{ReqEval},
	Summary:         "Per-row conditional sub-pipes",
	Description:     "Routes each row through `then` or `else` based on a DTL predicate. Live-safety propagates from the children.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"when", "then"},
		"properties": map[string]any{
			"when": map[string]any{
				"title":       "Predicate",
				"description": "DTL expression evaluated per row. Truthy results take the `then` branch.",
				"type":        "string",
				"x-dql-input": InputKindDTLExpression,
			},
			"then": map[string]any{
				"title":       "Then sub-pipe",
				"description": "Stages applied to rows where the predicate is truthy.",
				"type":        "array",
				"items":       refSchema("PipeStage"),
			},
			"else": map[string]any{
				"title":       "Else sub-pipe",
				"description": "Optional. Stages applied to rows where the predicate is falsy. Empty drops the row.",
				"type":        "array",
				"items":       refSchema("PipeStage"),
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"when", "then", "else"},
	},
}

var mergeMeta = OpMetadata{
	Name:            "merge",
	Summary:         "Fan-out then concat",
	Description:     "Runs each sub-pipe against a clone of the input and concatenates outputs.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"sources"},
		"properties": map[string]any{
			"sources": map[string]any{
				"title":       "Sub-pipes",
				"description": "List of sub-pipes. Each receives a clone of the input rows; outputs are concatenated in order.",
				"type":        "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"pipe"},
					"properties": map[string]any{
						"pipe": map[string]any{
							"title":       "Stages",
							"description": "Ordered stages for this branch.",
							"type":        "array",
							"items":       refSchema("PipeStage"),
						},
					},
					"x-dql-property-order": []string{"pipe"},
				},
				"minItems": 1,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"sources"},
	},
}

var tapMeta = OpMetadata{
	Name:            "tap",
	Summary:         "Pass-through with a debug label",
	Description:     "Records the row count against a label and returns rows unchanged.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"label": map[string]any{
				"title":       "Debug label",
				"description": "Label that surfaces in `stats.pipe[].label` so this checkpoint is identifiable in the response.",
				"type":        "string",
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"label"},
	},
}

// refSchema returns a $ref node pointing at a definition in the union schema.
func refSchema(name string) map[string]any {
	return map[string]any{"$ref": "#/$defs/" + name}
}

// withTitleDesc returns a shallow copy of schema with `title` and `description`
// set, leaving any existing properties untouched. Used to attach human labels
// to per-property schemas (including $ref nodes) so the frontend form-builder
// has a label and tooltip without mutating the shared definition.
func withTitleDesc(schema map[string]any, title, description string) map[string]any {
	out := make(map[string]any, len(schema)+2)
	for k, v := range schema {
		out[k] = v
	}
	if title != "" {
		out["title"] = title
	}
	if description != "" {
		out["description"] = description
	}
	return out
}

// --- Extended catalog entries (time, reshape, quality, set, histogram) ---

var timeBucketMeta = OpMetadata{
	Name:            "timeBucket",
	Summary:         "Bucket rows into fixed time intervals",
	Description:     "Adds a column with the start of the bucket containing each row's timestamp. Pair with `groupBy` + `aggregate` for time-series rollups.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"field", "interval", "as"},
		"properties": map[string]any{
			"field": map[string]any{
				"title":       "Timestamp column",
				"description": "Column carrying the row's timestamp.",
				"type":        "string",
				"x-dql-input": InputKindColumn,
			},
			"interval": map[string]any{
				"title":       "Bucket size",
				"description": "Duration string for the bucket width — `\"5m\"`, `\"1h\"`, `\"1d\"`.",
				"type":        "string",
				"x-dql-input": InputKindDuration,
			},
			"as": map[string]any{
				"title":       "Output column",
				"description": "Column to write the bucket-start timestamp into.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"tz": map[string]any{
				"title":       "Timezone",
				"description": "Optional IANA timezone (e.g. `America/New_York`) used to align bucket boundaries. Defaults to UTC.",
				"type":        "string",
				"x-dql-input": InputKindTimezone,
			},
			"origin": map[string]any{
				"title":       "Bucket origin",
				"description": "Optional RFC3339 anchor that defines where buckets start. Defaults to the Unix epoch.",
				"type":        "string",
				"x-dql-input": InputKindTimestamp,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"field", "interval", "as", "tz", "origin"},
	},
	Examples: []OpExample{
		{Title: "5-minute buckets", Config: map[string]any{
			"op": "timeBucket", "field": "ts", "interval": "5m", "as": "bucket",
		}},
	},
}

var gapfillMeta = OpMetadata{
	Name:            "gapfill",
	Summary:         "Fill missing time intervals",
	Description:     "Emits synthetic rows for time buckets missing from the input. Useful before charting sparse sensor streams.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"field", "interval"},
		"properties": map[string]any{
			"field": map[string]any{
				"title":       "Timestamp column",
				"description": "Column carrying the row's timestamp.",
				"type":        "string",
				"x-dql-input": InputKindColumn,
			},
			"interval": map[string]any{
				"title":       "Bucket size",
				"description": "Duration string (`\"5m\"`, `\"1h\"`) describing the gap between expected timestamps.",
				"type":        "string",
				"x-dql-input": InputKindDuration,
			},
			"from": map[string]any{
				"title":       "Range start",
				"description": "Optional RFC3339 lower bound. Defaults to the earliest timestamp in the input.",
				"type":        "string",
				"x-dql-input": InputKindTimestamp,
			},
			"to": map[string]any{
				"title":       "Range end",
				"description": "Optional RFC3339 upper bound. Defaults to the latest timestamp in the input.",
				"type":        "string",
				"x-dql-input": InputKindTimestamp,
			},
			"method": map[string]any{
				"title":       "Fill method",
				"description": "How to populate value columns on synthetic rows: `zero`, `null`, `lastValue` (carry forward), or `value` (use the literal `value` field).",
				"type":        "string",
				"enum":        []string{"zero", "null", "lastValue", "value"},
				"default":     "null",
			},
			"value": map[string]any{
				"title":               "Constant fill value",
				"description":         "Used when `method` is `value`. Any JSON scalar.",
				"x-dql-required-when": map[string]any{"method": []string{"value"}},
			},
			"groupBy": map[string]any{
				"title":       "Per-series partition",
				"description": "Optional. Treat the input as multiple independent time series, gapfilling each partition separately.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
			"carry": map[string]any{
				"title":               "Carry-forward columns",
				"description":         "Columns whose value is copied from the most recent real row when `method` is `lastValue`.",
				"type":                "array",
				"items":               map[string]any{"type": "string", "x-dql-input": InputKindColumn},
				"x-dql-required-when": map[string]any{"method": []string{"lastValue"}},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"field", "interval", "method", "value", "from", "to", "groupBy", "carry"},
	},
}

var asofJoinMeta = OpMetadata{
	Name:            "asofJoin",
	Requires:        []Requirement{ReqClassic},
	Summary:         "Join on the closest timestamp",
	Description:     "For each left row, finds the right row with the closest timestamp on a matching key. Direction backward (default) | forward | nearest, with optional tolerance.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"dataset", "leftTime", "rightTime"},
		"properties": map[string]any{
			"dataset": map[string]any{
				"title":       "Right-side dataset",
				"description": "Dataset to fetch the right side from.",
				"type":        "string",
				"x-dql-input": InputKindDataset,
			},
			"leftTime": map[string]any{
				"title":       "Left timestamp column",
				"description": "Column on the streaming (left) side carrying the row's timestamp.",
				"type":        "string",
				"x-dql-input": InputKindColumn,
			},
			"rightTime": map[string]any{
				"title":       "Right timestamp column",
				"description": "Column on the looked-up (right) side carrying the row's timestamp.",
				"type":        "string",
				"x-dql-input": InputKindDatasetColumn,
			},
			"leftKey": map[string]any{
				"title":       "Left identity column",
				"description": "Optional. Column on the left whose value must equal `rightKey` for a match.",
				"type":        "string",
				"x-dql-input": InputKindColumn,
			},
			"rightKey": map[string]any{
				"title":       "Right identity column",
				"description": "Optional. Column on the right whose value must equal `leftKey` for a match.",
				"type":        "string",
				"x-dql-input": InputKindDatasetColumn,
			},
			"tolerance": map[string]any{
				"title":       "Match tolerance",
				"description": "Optional duration (`\"1m\"`, `\"30s\"`). Drops matches further than this from the left row's timestamp.",
				"type":        "string",
				"x-dql-input": InputKindDuration,
			},
			"direction": map[string]any{
				"title":       "Search direction",
				"description": "`backward` finds the latest right row at or before the left row; `forward` the earliest at or after; `nearest` the absolute closest.",
				"type":        "string",
				"enum":        []string{"backward", "forward", "nearest"},
				"default":     "backward",
			},
			"as": map[string]any{
				"title":       "Output column",
				"description": "Optional. When set, the matched right row is nested under this column.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"select": map[string]any{
				"title":       "Right columns to keep",
				"description": "Optional subset of right-side columns to merge in. Empty means all columns.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindDatasetColumn},
			},
			"where": withTitleDesc(
				refSchema("WhereClause"),
				"Right-side filter",
				"Optional WHERE clause applied to the right-side dataset before the join.",
			),
			"limit": map[string]any{
				"title":       "Right-side row cap",
				"description": "Optional cap on right-side rows fetched.",
				"type":        "integer",
				"minimum":     0,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"dataset", "leftTime", "rightTime", "leftKey", "rightKey", "direction", "tolerance", "as", "select", "where", "limit"},
	},
}

var topPerGroupMeta = OpMetadata{
	Name:            "topPerGroup",
	Summary:         "Top N rows per partition",
	Description:     "Keeps the highest- (or lowest-) ranked N rows per group. With no partition, equivalent to top N overall.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"n", "by"},
		"properties": map[string]any{
			"n": map[string]any{
				"title":       "Rows per partition",
				"description": "How many rows to keep per partition (1 = top-1).",
				"type":        "integer",
				"minimum":     1,
			},
			"by": map[string]any{
				"title":       "Ranking keys",
				"description": "Sort keys used to rank rows within each partition. The first key is primary; later keys break ties.",
				"type":        "array",
				"items":       refSchema("OrderByClause"),
				"minItems":    1,
			},
			"partitionBy": map[string]any{
				"title":       "Partition columns",
				"description": "Optional. Columns that split the input into partitions. Empty = single partition (top-N overall).",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"n", "partitionBy", "by"},
	},
}

var pivotMeta = OpMetadata{
	Name:            "pivot",
	Summary:         "Rows → columns",
	Description:     "Each unique value in `columnKey` becomes a column. `aggregate` controls how to combine duplicates (sum/avg/count/min/max/first/last).",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"columnKey", "valueField"},
		"properties": map[string]any{
			"rowKeys": map[string]any{
				"title":       "Row keys",
				"description": "Columns whose distinct combinations identify each output row.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
			"columnKey": map[string]any{
				"title":       "Column key",
				"description": "Column whose distinct values become the names of new columns.",
				"type":        "string",
				"x-dql-input": InputKindColumn,
			},
			"valueField": map[string]any{
				"title":       "Value column",
				"description": "Column whose values populate the cells.",
				"type":        "string",
				"x-dql-input": InputKindColumn,
			},
			"aggregate": map[string]any{
				"title":       "Aggregation",
				"description": "How to combine multiple input rows that map to the same cell.",
				"type":        "string",
				"enum":        []string{"sum", "avg", "count", "min", "max", "first", "last"},
				"default":     "first",
			},
			"fillValue": map[string]any{
				"title":       "Fill value",
				"description": "Value to use for output cells with no source row. Defaults to null.",
			},
			"prefix": map[string]any{
				"title":       "Column prefix",
				"description": "Optional string prepended to every generated column name.",
				"type":        "string",
				"x-dql-input": InputKindPrefix,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"rowKeys", "columnKey", "valueField", "aggregate", "fillValue", "prefix"},
	},
}

var unpivotMeta = OpMetadata{
	Name:            "unpivot",
	Summary:         "Columns → rows",
	Description:     "Melts named columns into name/value pairs (one output row per input row × valueCol).",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"nameAs", "valueAs"},
		"properties": map[string]any{
			"idCols": map[string]any{
				"title":       "Identity columns",
				"description": "Columns kept verbatim on every output row. Empty means every column not in `valueCols`.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
			"valueCols": map[string]any{
				"title":       "Value columns to melt",
				"description": "Columns whose values are unstacked into rows. Empty means every column not in `idCols`.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
			"nameAs": map[string]any{
				"title":       "Name output column",
				"description": "Output column that records the source column name.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"valueAs": map[string]any{
				"title":       "Value output column",
				"description": "Output column that records the cell value.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"idCols", "valueCols", "nameAs", "valueAs"},
	},
}

var unnestObjectMeta = OpMetadata{
	Name:            "unnestObject",
	Summary:         "Spread an object column into root keys",
	Description:     "Useful for JSONB/metadata blobs whose nested keys you want as top-level columns.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"field"},
		"properties": map[string]any{
			"field": map[string]any{
				"title":       "Object column",
				"description": "Column whose object value's keys are spread onto the root row.",
				"type":        "string",
				"x-dql-input": InputKindColumn,
			},
			"prefix": map[string]any{
				"title":       "Key prefix",
				"description": "Optional. Prepended to every spread key (e.g. `m_` produces `m_owner`).",
				"type":        "string",
				"x-dql-input": InputKindPrefix,
			},
			"drop": map[string]any{
				"title":       "Drop source column",
				"description": "When true, the original object column is removed after spreading.",
				"type":        "boolean",
				"default":     false,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"field", "prefix", "drop"},
	},
}

var nestMeta = OpMetadata{
	Name:            "nest",
	Summary:         "Group rows into nested arrays",
	Description:     "Inverse of `flatten`: emits one row per group with the remaining columns collected into an array under `into`.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"by", "into"},
		"properties": map[string]any{
			"by": map[string]any{
				"title":       "Group keys",
				"description": "Partition keys; one output row is emitted per unique combination.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
				"minItems":    1,
			},
			"into": map[string]any{
				"title":       "Nested array column",
				"description": "Name of the output column where the per-group array of nested records is stored.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"include": map[string]any{
				"title":       "Columns to nest",
				"description": "Optional. Columns to include in each nested record. Defaults to every column not in `by`.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"by", "into", "include"},
	},
}

var dropNullsMeta = OpMetadata{
	Name:            "dropNulls",
	Summary:         "Drop rows with nulls in named columns",
	Description:     "When `any` is true (default) drops rows where ANY listed column is null; when false drops only when EVERY listed column is null.",
	LiveSafeDefault: true,
	Pushable:        true,
	ConfigSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"columns": map[string]any{
				"title":       "Columns to check",
				"description": "Columns inspected for null. Empty means every column on each row.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
			"any": map[string]any{
				"title":       "Drop on any null",
				"description": "When true, drop a row if ANY listed column is null. When false, drop only when EVERY listed column is null.",
				"type":        "boolean",
				"default":     true,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"columns", "any"},
	},
}

var fillNullsMeta = OpMetadata{
	Name:            "fillNulls",
	Summary:         "Replace nulls in named columns",
	Description:     "Methods: zero, value, lastValue, nextValue, mean. lastValue/nextValue require partitionBy + orderBy for deterministic forward/backward fill.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"columns": map[string]any{
				"title":       "Columns to fill",
				"description": "Columns whose null cells are replaced. Empty means every column on each row.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
			"method": map[string]any{
				"title":       "Fill method",
				"description": "`value` uses the literal `value`; `zero` uses 0; `lastValue` carries the previous non-null forward; `nextValue` carries the next non-null backward; `mean` uses the column mean.",
				"type":        "string",
				"enum":        []string{"value", "zero", "lastValue", "nextValue", "mean"},
				"default":     "value",
			},
			"value": map[string]any{
				"title":               "Constant fill value",
				"description":         "Used when `method` is `value`. Any JSON scalar.",
				"x-dql-required-when": map[string]any{"method": []string{"value"}},
			},
			"partitionBy": map[string]any{
				"title":               "Partition keys",
				"description":         "Required for `lastValue` / `nextValue`. Within each partition, ordering and carry semantics apply independently.",
				"type":                "array",
				"items":               map[string]any{"type": "string", "x-dql-input": InputKindColumn},
				"x-dql-required-when": map[string]any{"method": []string{"lastValue", "nextValue"}},
			},
			"orderBy": map[string]any{
				"title":               "Sort order",
				"description":         "Required for `lastValue` / `nextValue` to define what \"previous\" / \"next\" means within a partition.",
				"type":                "array",
				"items":               refSchema("OrderByClause"),
				"x-dql-required-when": map[string]any{"method": []string{"lastValue", "nextValue"}},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"method", "value", "columns", "partitionBy", "orderBy"},
	},
}

var transformMeta = OpMetadata{
	Name:            "transform",
	Requires:        []Requirement{ReqEval},
	Summary:         "Compute multiple columns in one stage",
	Description:     "Like `compute` but takes a list. Entries evaluate in order and can reference earlier results. Use `from` for plain column copies and `replace: true` to drop everything not produced.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"compute"},
		"properties": map[string]any{
			"compute": map[string]any{
				"title":       "Computed columns",
				"description": "Ordered list of column definitions. Each entry uses `expr` (DTL) or `from` (column copy).",
				"type":        "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"as"},
					"properties": map[string]any{
						"as": map[string]any{
							"title":       "Output column name",
							"description": "Column to write the value into.",
							"type":        "string",
							"x-dql-input": InputKindColumnOutput,
						},
						"expr": map[string]any{
							"title":       "DTL expression",
							"description": "DTL expression evaluated against each row. Mutually exclusive with `from`.",
							"type":        "string",
							"x-dql-input": InputKindDTLExpression,
						},
						"from": map[string]any{
							"title":       "Source column",
							"description": "Existing column to copy verbatim. Mutually exclusive with `expr`.",
							"type":        "string",
							"x-dql-input": InputKindColumn,
						},
					},
					"x-dql-property-order": []string{"as", "expr", "from"},
				},
				"minItems": 1,
			},
			"drop": map[string]any{
				"title":       "Columns to drop",
				"description": "Optional. Columns to remove from the output after computation.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
			"replace": map[string]any{
				"title":       "Replace mode",
				"description": "When true, drop every column not produced by `compute`. Useful for reshape-and-rename in one step.",
				"type":        "boolean",
				"default":     false,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"compute", "drop", "replace"},
	},
	Examples: []OpExample{
		{Title: "Add several columns", Config: map[string]any{
			"op": "transform", "compute": []map[string]any{
				{"as": "total", "expr": "a + b"},
				{"as": "ratio", "expr": "a / b"},
			},
		}},
		{Title: "Reshape", Config: map[string]any{
			"op": "transform", "replace": true, "compute": []map[string]any{
				{"as": "id", "from": "_id"},
				{"as": "score", "expr": "wins / matches"},
			},
		}},
	},
}

var castMeta = OpMetadata{
	Name:            "cast",
	Summary:         "Convert column types",
	Description:     "Coerce columns to int / float / bool / string / timestamp. onError chooses null (default) | skip | fail.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"casts"},
		"properties": map[string]any{
			"casts": map[string]any{
				"title":       "Casts",
				"description": "List of `{field, to, onError?}` rules applied in order.",
				"type":        "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"field", "to"},
					"properties": map[string]any{
						"field": map[string]any{
							"title":       "Column",
							"description": "Column whose values should be cast.",
							"type":        "string",
							"x-dql-input": InputKindColumn,
						},
						"to": map[string]any{
							"title":       "Target type",
							"description": "Type to coerce values into.",
							"type":        "string",
							"enum":        []string{"int", "float", "bool", "string", "timestamp"},
						},
						"onError": map[string]any{
							"title":       "On parse error",
							"description": "How to handle uncastable values: `null` writes null; `skip` keeps the original value; `fail` errors the whole query.",
							"type":        "string",
							"enum":        []string{"null", "skip", "fail"},
							"default":     "null",
						},
					},
					"x-dql-property-order": []string{"field", "to", "onError"},
				},
				"minItems": 1,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"casts"},
	},
}

var dedupeMeta = OpMetadata{
	Name:            "dedupe",
	Summary:         "Keep one row per identity key",
	Description:     "Different from `distinct` (full-row equality): pick first or last row per key, with optional ordering.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"by"},
		"properties": map[string]any{
			"by": map[string]any{
				"title":       "Identity columns",
				"description": "Columns whose combined values define a row's identity.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
				"minItems":    1,
			},
			"keep": map[string]any{
				"title":       "Which to keep",
				"description": "Whether to keep the first row encountered per identity (`first`) or the last (`last`). Combined with `orderBy` for deterministic results.",
				"type":        "string",
				"enum":        []string{"first", "last"},
				"default":     "first",
			},
			"orderBy": map[string]any{
				"title":       "Sort order",
				"description": "Optional. Sort applied within each identity group before applying `keep`.",
				"type":        "array",
				"items":       refSchema("OrderByClause"),
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"by", "keep", "orderBy"},
	},
}

var sampleMeta = OpMetadata{
	Name:            "sample",
	Summary:         "Random subset of rows",
	Description:     "Either `n` (target size) or `ratio` (0..1). `seed` makes output deterministic. Method: random (reservoir) | systematic (every k-th).",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"n": map[string]any{
				"title":       "Target row count",
				"description": "Sample size in rows. Mutually exclusive with `ratio`.",
				"type":        "integer",
				"minimum":     1,
			},
			"ratio": map[string]any{
				"title":       "Sample ratio",
				"description": "Fraction of input rows to keep, between 0 and 1. Mutually exclusive with `n`.",
				"type":        "number",
				"minimum":     0,
				"maximum":     1,
			},
			"seed": map[string]any{
				"title":       "Random seed",
				"description": "Optional integer seed for deterministic sampling. Same seed + same input ⇒ same output.",
				"type":        "integer",
			},
			"method": map[string]any{
				"title":       "Sampling method",
				"description": "`random` uses reservoir sampling; `systematic` keeps every k-th row.",
				"type":        "string",
				"enum":        []string{"random", "systematic"},
				"default":     "random",
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"method", "n", "ratio", "seed"},
	},
}

var assertMeta = OpMetadata{
	Name:            "assert",
	Requires:        []Requirement{ReqEval},
	Summary:         "Fail the query when an expression is false",
	Description:     "Runtime guardrail. scope=row evaluates per row; scope=overall checks once with `count` available.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"expr"},
		"properties": map[string]any{
			"expr": map[string]any{
				"title":       "Assertion expression",
				"description": "DTL expression that must be truthy. Falsy results fail the query.",
				"type":        "string",
				"x-dql-input": InputKindDTLExpression,
			},
			"scope": map[string]any{
				"title":       "Scope",
				"description": "`row` evaluates the expression per row; `overall` evaluates once at the end with the row `count` available.",
				"type":        "string",
				"enum":        []string{"row", "overall"},
				"default":     "row",
			},
			"message": map[string]any{
				"title":       "Failure message",
				"description": "Custom error message returned when the assertion fails.",
				"type":        "string",
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"scope", "expr", "message"},
	},
}

var intersectMeta = OpMetadata{
	Name:            "intersect",
	Requires:        []Requirement{ReqClassic},
	Summary:         "Rows present in every source",
	Description:     "Set intersection across N sub-pipes, identified by `by` keys (or full-row equality).",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"sources"},
		"properties": map[string]any{
			"sources": map[string]any{
				"title":       "Sub-pipes",
				"description": "Two or more sub-pipes. Output rows are those present in every sub-pipe (under the chosen identity).",
				"type":        "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"pipe"},
					"properties": map[string]any{
						"pipe": map[string]any{
							"title":       "Stages",
							"description": "Ordered stages for this branch.",
							"type":        "array",
							"items":       refSchema("PipeStage"),
						},
					},
					"x-dql-property-order": []string{"pipe"},
				},
				"minItems": 2,
			},
			"by": map[string]any{
				"title":       "Identity columns",
				"description": "Optional. When set, intersection compares only these columns. Empty falls back to full-row equality.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"sources", "by"},
	},
}

var exceptMeta = OpMetadata{
	Name:            "except",
	Requires:        []Requirement{ReqClassic},
	Summary:         "Rows in left but not in right",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"left", "right"},
		"properties": map[string]any{
			"left": map[string]any{
				"title":       "Left sub-pipe",
				"description": "Source whose rows are kept when not present in `right`.",
				"type":        "object",
				"required":    []string{"pipe"},
				"properties": map[string]any{
					"pipe": map[string]any{
						"title":       "Stages",
						"description": "Ordered stages for the left branch.",
						"type":        "array",
						"items":       refSchema("PipeStage"),
					},
				},
				"x-dql-property-order": []string{"pipe"},
			},
			"right": map[string]any{
				"title":       "Right sub-pipe",
				"description": "Source whose rows are subtracted from `left`.",
				"type":        "object",
				"required":    []string{"pipe"},
				"properties": map[string]any{
					"pipe": map[string]any{
						"title":       "Stages",
						"description": "Ordered stages for the right branch.",
						"type":        "array",
						"items":       refSchema("PipeStage"),
					},
				},
				"x-dql-property-order": []string{"pipe"},
			},
			"by": map[string]any{
				"title":       "Identity columns",
				"description": "Optional. When set, set difference compares only these columns. Empty falls back to full-row equality.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindColumn},
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"left", "right", "by"},
	},
}

var crossJoinMeta = OpMetadata{
	Name:            "crossJoin",
	Requires:        []Requirement{ReqClassic},
	Summary:         "Cartesian product with another dataset",
	Description:     "Output size is N × M — use sparingly. Right-side rows can be filtered with `where` and capped with `limit`.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"dataset"},
		"properties": map[string]any{
			"dataset": map[string]any{
				"title":       "Right-side dataset",
				"description": "Dataset to fetch the cartesian-product right side from.",
				"type":        "string",
				"x-dql-input": InputKindDataset,
			},
			"as": map[string]any{
				"title":       "Output column",
				"description": "Optional. When set, each right row is nested under this column instead of being flattened into the left row.",
				"type":        "string",
				"x-dql-input": InputKindColumnOutput,
			},
			"select": map[string]any{
				"title":       "Right columns to keep",
				"description": "Optional subset of right-side columns to merge in. Empty means all columns.",
				"type":        "array",
				"items":       map[string]any{"type": "string", "x-dql-input": InputKindDatasetColumn},
			},
			"where": withTitleDesc(
				refSchema("WhereClause"),
				"Right-side filter",
				"Optional WHERE clause applied to the right-side dataset before the cross-join.",
			),
			"limit": map[string]any{
				"title":       "Right-side row cap",
				"description": "Optional cap on right-side rows. Strongly recommended to bound output size.",
				"type":        "integer",
				"minimum":     0,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"dataset", "as", "select", "where", "limit"},
	},
}

var histogramMeta = OpMetadata{
	Name:            "histogram",
	Summary:         "Bucket numeric values into equal-width bins",
	Description:     "Replaces input rows with one row per bin: {binStart, binEnd, count}. Provide explicit min/max or let the op derive them.",
	LiveSafeDefault: true,
	Pushable:        false,
	ConfigSchema: map[string]any{
		"type":     "object",
		"required": []string{"field", "bins"},
		"properties": map[string]any{
			"field": map[string]any{
				"title":       "Numeric column",
				"description": "Column whose numeric values are bucketed.",
				"type":        "string",
				"x-dql-input": InputKindColumn,
			},
			"bins": map[string]any{
				"title":       "Bin count",
				"description": "Number of equal-width bins to produce.",
				"type":        "integer",
				"minimum":     1,
			},
			"min": map[string]any{
				"title":       "Range minimum",
				"description": "Optional lower bound. Defaults to the minimum value seen in the input.",
				"type":        "number",
			},
			"max": map[string]any{
				"title":       "Range maximum",
				"description": "Optional upper bound. Defaults to the maximum value seen in the input.",
				"type":        "number",
			},
			"asCount": map[string]any{
				"title":       "Count column name",
				"description": "Output column for the per-bin count.",
				"type":        "string",
				"default":     "count",
				"x-dql-input": InputKindColumnOutput,
			},
			"asStart": map[string]any{
				"title":       "Bin-start column name",
				"description": "Output column for each bin's lower edge.",
				"type":        "string",
				"default":     "binStart",
				"x-dql-input": InputKindColumnOutput,
			},
			"asEnd": map[string]any{
				"title":       "Bin-end column name",
				"description": "Output column for each bin's upper edge.",
				"type":        "string",
				"default":     "binEnd",
				"x-dql-input": InputKindColumnOutput,
			},
		},
		"additionalProperties": false,
		"x-dql-property-order": []string{"field", "bins", "min", "max", "asCount", "asStart", "asEnd"},
	},
}
