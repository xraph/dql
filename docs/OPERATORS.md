# DQL pipe operators

Every operator in the pipe catalog. **This file is generated** from
`pipe.Reference()` — edit `pipe/catalog.go` and run `make generate`.

A pipe is an ordered chain of these operators applied to a stream of rows.
They appear below in catalog order, which follows the shape of a typical
pipe: source, filter, transform, aggregate, sort, side effect.

## Column key

| Column | Meaning |
|---|---|
| **Live-safe** | Pure and deterministic with default config, so it can run on a live-updating result set. |
| **Pushable** | Can fold into the SQL prefix. The planner makes the final call from the field types. |
| **Requires** | Host services the operator needs in its `OpContext`. Blank means it works anywhere. |

## Index

| Operator | Summary | Live-safe | Pushable | Requires |
|---|---|---|---|---|
| [`filter`](#filter) | Keep rows that match a predicate | ✅ | ✅ | `eval` |
| [`project`](#project) | Select / alias columns | ✅ | ✅ |  |
| [`rename`](#rename) | Rename columns | ✅ | ✅ |  |
| [`drop`](#drop) | Remove columns | ✅ | — |  |
| [`compute`](#compute) | Add a computed column | ✅ | — | `eval`, `formula` |
| [`transform`](#transform) | Compute multiple columns in one stage | ✅ | — | `eval` |
| [`cast`](#cast) | Convert column types | ✅ | — |  |
| [`dropNulls`](#dropnulls) | Drop rows with nulls in named columns | ✅ | ✅ |  |
| [`fillNulls`](#fillnulls) | Replace nulls in named columns | ✅ | — |  |
| [`flatten`](#flatten) | Unnest an array column | ✅ | — |  |
| [`unnestObject`](#unnestobject) | Spread an object column into root keys | ✅ | — |  |
| [`nest`](#nest) | Group rows into nested arrays | ✅ | — |  |
| [`pivot`](#pivot) | Rows → columns | ✅ | — |  |
| [`unpivot`](#unpivot) | Columns → rows | ✅ | — |  |
| [`distinct`](#distinct) | Deduplicate rows | ✅ | — |  |
| [`dedupe`](#dedupe) | Keep one row per identity key | ✅ | — |  |
| [`sample`](#sample) | Random subset of rows | ✅ | — |  |
| [`assert`](#assert) | Fail the query when an expression is false | ✅ | — | `eval` |
| [`timeBucket`](#timebucket) | Bucket rows into fixed time intervals | ✅ | — |  |
| [`gapfill`](#gapfill) | Fill missing time intervals | ✅ | — |  |
| [`sort`](#sort) | Order rows | ✅ | ✅ |  |
| [`limit`](#limit) | Cap row count | ✅ | ✅ |  |
| [`skip`](#skip) | Drop the first N rows | ✅ | ✅ |  |
| [`topPerGroup`](#toppergroup) | Top N rows per partition | ✅ | — |  |
| [`groupBy`](#groupby) | Set keys for the next aggregate | ✅ | ✅ |  |
| [`aggregate`](#aggregate) | Fold rows into groups | ✅ | ✅ |  |
| [`histogram`](#histogram) | Bucket numeric values into equal-width bins | ✅ | — |  |
| [`window`](#window) | Per-row computation within a partition | ✅ | — |  |
| [`lookup`](#lookup) | Hash-join with another dataset | ✅ | — | `classic` |
| [`asofJoin`](#asofjoin) | Join on the closest timestamp | ✅ | — | `classic` |
| [`crossJoin`](#crossjoin) | Cartesian product with another dataset | ✅ | — | `classic` |
| [`intersect`](#intersect) | Rows present in every source | ✅ | — | `classic` |
| [`except`](#except) | Rows in left but not in right | ✅ | — | `classic` |
| [`callFunction`](#callfunction) | Invoke a registered DTL function | — | — | `functionRegistry` |
| [`callApp`](#callapp) | Invoke a managed app | — | — | `appCaller` |
| [`algo`](#algo) | Run a registered algorithm | — | — | `algorithms` |
| [`branch`](#branch) | Per-row conditional sub-pipes | ✅ | — | `eval` |
| [`merge`](#merge) | Fan-out then concat | ✅ | — |  |
| [`tap`](#tap) | Pass-through with a debug label | ✅ | — |  |

---

## `filter`

Keep rows that match a predicate

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* yes &nbsp;&nbsp; *Requires:* `eval`

Filters rows by a WHERE clause. Plain field/op/value forms push down to SQL; DTL `expr` forms run in-memory.

| Field | Type | Required | Description |
|---|---|---|---|
| `where` | any | yes | Conditions a row must satisfy to be kept. Use simple `{field, op, value}` shape for SQL-pushable filters, or `{expr: "…"}` for DTL expressions evaluated in-memory. |

**Plain comparison**

```json
{
  "op": "filter",
  "where": {
    "field": "level",
    "op": "==",
    "value": "ERROR"
  }
}
```

**DTL expression**

```json
{
  "op": "filter",
  "where": {
    "expr": "ts > now() - duration(\"1h\")"
  }
}
```

---

## `project`

Select / alias columns

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* yes

Reduces each row to a chosen subset of columns, optionally renaming them.

| Field | Type | Required | Description |
|---|---|---|---|
| `select` | array |  | Columns to keep, in order. Each entry is `{field, as?}` — set `as` to rename. |
| `drop` | array of string |  | Alternative to `select` — instead of listing what to keep, list what to remove. Mutually exclusive with `select`. |

**Pick fields**

```json
{
  "op": "project",
  "select": [
    {
      "field": "id"
    },
    {
      "field": "name"
    }
  ]
}
```

---

## `rename`

Rename columns

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* yes

Rewrites column keys in-place. The map is `from -> to`.

| Field | Type | Required | Description |
|---|---|---|---|
| `map` | object | yes | Object whose keys are the existing column names and whose values are the new names. Pairs not listed are left untouched. (input: column-rename-map) |

**id → key**

```json
{
  "map": {
    "id": "key"
  },
  "op": "rename"
}
```

---

## `drop`

Remove columns

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Deletes the named columns from each row.

| Field | Type | Required | Description |
|---|---|---|---|
| `columns` | array of string | yes | Names of columns to remove from every row. |

**Drop secrets**

```json
{
  "columns": [
    "password",
    "secret"
  ],
  "op": "drop"
}
```

---

## `compute`

Add a computed column

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `eval`, `formula`

Evaluates a DTL expression (`kind:"expr"`) or Excel-style formula (`kind:"formula"`) per row and stores the result under `as`.

| Field | Type | Required | Description |
|---|---|---|---|
| `as` | string | yes | Column to write the computed value into. Overwrites any existing column with the same name. (input: column-output) |
| `kind` | string |  | Which language `expr` / `formula` is in. Defaults to DTL `expr`. Default: `expr`. |
| `expr` | string | conditional {"kind":["expr",""]} | DTL expression evaluated against each row. Used when `kind` is `expr`. (input: dtl-expression) |
| `formula` | string | conditional {"kind":["formula"]} | Spreadsheet formula evaluated against each row. Used when `kind` is `formula`. (input: formula) |

**DTL expr**

```json
{
  "as": "doubled",
  "expr": "value * 2",
  "op": "compute"
}
```

**Formula**

```json
{
  "as": "tax",
  "formula": "price * 0.1",
  "kind": "formula",
  "op": "compute"
}
```

---

## `transform`

Compute multiple columns in one stage

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `eval`

Like `compute` but takes a list. Entries evaluate in order and can reference earlier results. Use `from` for plain column copies and `replace: true` to drop everything not produced.

| Field | Type | Required | Description |
|---|---|---|---|
| `compute` | array of object | yes | Ordered list of column definitions. Each entry uses `expr` (DTL) or `from` (column copy). |
| `drop` | array of string |  | Optional. Columns to remove from the output after computation. |
| `replace` | boolean |  | When true, drop every column not produced by `compute`. Useful for reshape-and-rename in one step. Default: `false`. |

**Add several columns**

```json
{
  "compute": [
    {
      "as": "total",
      "expr": "a + b"
    },
    {
      "as": "ratio",
      "expr": "a / b"
    }
  ],
  "op": "transform"
}
```

**Reshape**

```json
{
  "compute": [
    {
      "as": "id",
      "from": "_id"
    },
    {
      "as": "score",
      "expr": "wins / matches"
    }
  ],
  "op": "transform",
  "replace": true
}
```

---

## `cast`

Convert column types

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Coerce columns to int / float / bool / string / timestamp. onError chooses null (default) | skip | fail.

| Field | Type | Required | Description |
|---|---|---|---|
| `casts` | array of object | yes | List of `{field, to, onError?}` rules applied in order. |

---

## `dropNulls`

Drop rows with nulls in named columns

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* yes

When `any` is true (default) drops rows where ANY listed column is null; when false drops only when EVERY listed column is null.

| Field | Type | Required | Description |
|---|---|---|---|
| `columns` | array of string |  | Columns inspected for null. Empty means every column on each row. |
| `any` | boolean |  | When true, drop a row if ANY listed column is null. When false, drop only when EVERY listed column is null. Default: `true`. |

---

## `fillNulls`

Replace nulls in named columns

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Methods: zero, value, lastValue, nextValue, mean. lastValue/nextValue require partitionBy + orderBy for deterministic forward/backward fill.

| Field | Type | Required | Description |
|---|---|---|---|
| `method` | string |  | `value` uses the literal `value`; `zero` uses 0; `lastValue` carries the previous non-null forward; `nextValue` carries the next non-null backward; `mean` uses the column mean. Default: `value`. |
| `value` | any | conditional {"method":["value"]} | Used when `method` is `value`. Any JSON scalar. |
| `columns` | array of string |  | Columns whose null cells are replaced. Empty means every column on each row. |
| `partitionBy` | array of string | conditional {"method":["lastValue","nextValue"]} | Required for `lastValue` / `nextValue`. Within each partition, ordering and carry semantics apply independently. |
| `orderBy` | array | conditional {"method":["lastValue","nextValue"]} | Required for `lastValue` / `nextValue` to define what "previous" / "next" means within a partition. |

---

## `flatten`

Unnest an array column

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Emits one row per array element. When `as` is set, the element is stored under that key and the original field is preserved.

| Field | Type | Required | Description |
|---|---|---|---|
| `field` | string | yes | Column whose array values are exploded into separate rows. (input: column) |
| `as` | string |  | Optional. Column name to store each element under. When unset, the element replaces the array column on each row. (input: column-output) |
| `indexAs` | string |  | Optional. Column name to store the element's 0-based index in the source array. (input: column-output) |
| `preserveEmpty` | boolean |  | When true, rows whose array is empty or null still produce one output row (with the element column nil). Default drops them. Default: `false`. |

**Unnest tags**

```json
{
  "field": "tags",
  "op": "flatten"
}
```

---

## `unnestObject`

Spread an object column into root keys

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Useful for JSONB/metadata blobs whose nested keys you want as top-level columns.

| Field | Type | Required | Description |
|---|---|---|---|
| `field` | string | yes | Column whose object value's keys are spread onto the root row. (input: column) |
| `prefix` | string |  | Optional. Prepended to every spread key (e.g. `m_` produces `m_owner`). (input: prefix) |
| `drop` | boolean |  | When true, the original object column is removed after spreading. Default: `false`. |

---

## `nest`

Group rows into nested arrays

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Inverse of `flatten`: emits one row per group with the remaining columns collected into an array under `into`.

| Field | Type | Required | Description |
|---|---|---|---|
| `by` | array of string | yes | Partition keys; one output row is emitted per unique combination. |
| `into` | string | yes | Name of the output column where the per-group array of nested records is stored. (input: column-output) |
| `include` | array of string |  | Optional. Columns to include in each nested record. Defaults to every column not in `by`. |

---

## `pivot`

Rows → columns

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Each unique value in `columnKey` becomes a column. `aggregate` controls how to combine duplicates (sum/avg/count/min/max/first/last).

| Field | Type | Required | Description |
|---|---|---|---|
| `rowKeys` | array of string |  | Columns whose distinct combinations identify each output row. |
| `columnKey` | string | yes | Column whose distinct values become the names of new columns. (input: column) |
| `valueField` | string | yes | Column whose values populate the cells. (input: column) |
| `aggregate` | string |  | How to combine multiple input rows that map to the same cell. Default: `first`. |
| `fillValue` | any |  | Value to use for output cells with no source row. Defaults to null. |
| `prefix` | string |  | Optional string prepended to every generated column name. (input: prefix) |

---

## `unpivot`

Columns → rows

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Melts named columns into name/value pairs (one output row per input row × valueCol).

| Field | Type | Required | Description |
|---|---|---|---|
| `idCols` | array of string |  | Columns kept verbatim on every output row. Empty means every column not in `valueCols`. |
| `valueCols` | array of string |  | Columns whose values are unstacked into rows. Empty means every column not in `idCols`. |
| `nameAs` | string | yes | Output column that records the source column name. (input: column-output) |
| `valueAs` | string | yes | Output column that records the cell value. (input: column-output) |

---

## `distinct`

Deduplicate rows

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

When `by` is empty, uniqueness uses every column; otherwise only the named keys.

| Field | Type | Required | Description |
|---|---|---|---|
| `by` | array of string |  | Columns whose combined values define a row's identity. Leave empty to deduplicate using every column. |

---

## `dedupe`

Keep one row per identity key

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Different from `distinct` (full-row equality): pick first or last row per key, with optional ordering.

| Field | Type | Required | Description |
|---|---|---|---|
| `by` | array of string | yes | Columns whose combined values define a row's identity. |
| `keep` | string |  | Whether to keep the first row encountered per identity (`first`) or the last (`last`). Combined with `orderBy` for deterministic results. Default: `first`. |
| `orderBy` | array |  | Optional. Sort applied within each identity group before applying `keep`. |

---

## `sample`

Random subset of rows

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Either `n` (target size) or `ratio` (0..1). `seed` makes output deterministic. Method: random (reservoir) | systematic (every k-th).

| Field | Type | Required | Description |
|---|---|---|---|
| `method` | string |  | `random` uses reservoir sampling; `systematic` keeps every k-th row. Default: `random`. |
| `n` | integer |  | Sample size in rows. Mutually exclusive with `ratio`. |
| `ratio` | number |  | Fraction of input rows to keep, between 0 and 1. Mutually exclusive with `n`. |
| `seed` | integer |  | Optional integer seed for deterministic sampling. Same seed + same input ⇒ same output. |

---

## `assert`

Fail the query when an expression is false

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `eval`

Runtime guardrail. scope=row evaluates per row; scope=overall checks once with `count` available.

| Field | Type | Required | Description |
|---|---|---|---|
| `scope` | string |  | `row` evaluates the expression per row; `overall` evaluates once at the end with the row `count` available. Default: `row`. |
| `expr` | string | yes | DTL expression that must be truthy. Falsy results fail the query. (input: dtl-expression) |
| `message` | string |  | Custom error message returned when the assertion fails. |

---

## `timeBucket`

Bucket rows into fixed time intervals

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Adds a column with the start of the bucket containing each row's timestamp. Pair with `groupBy` + `aggregate` for time-series rollups.

| Field | Type | Required | Description |
|---|---|---|---|
| `field` | string | yes | Column carrying the row's timestamp. (input: column) |
| `interval` | string | yes | Duration string for the bucket width — `"5m"`, `"1h"`, `"1d"`. (input: duration) |
| `as` | string | yes | Column to write the bucket-start timestamp into. (input: column-output) |
| `tz` | string |  | Optional IANA timezone (e.g. `America/New_York`) used to align bucket boundaries. Defaults to UTC. (input: timezone) |
| `origin` | string |  | Optional RFC3339 anchor that defines where buckets start. Defaults to the Unix epoch. (input: timestamp) |

**5-minute buckets**

```json
{
  "as": "bucket",
  "field": "ts",
  "interval": "5m",
  "op": "timeBucket"
}
```

---

## `gapfill`

Fill missing time intervals

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Emits synthetic rows for time buckets missing from the input. Useful before charting sparse sensor streams.

| Field | Type | Required | Description |
|---|---|---|---|
| `field` | string | yes | Column carrying the row's timestamp. (input: column) |
| `interval` | string | yes | Duration string (`"5m"`, `"1h"`) describing the gap between expected timestamps. (input: duration) |
| `method` | string |  | How to populate value columns on synthetic rows: `zero`, `null`, `lastValue` (carry forward), or `value` (use the literal `value` field). Default: `null`. |
| `value` | any | conditional {"method":["value"]} | Used when `method` is `value`. Any JSON scalar. |
| `from` | string |  | Optional RFC3339 lower bound. Defaults to the earliest timestamp in the input. (input: timestamp) |
| `to` | string |  | Optional RFC3339 upper bound. Defaults to the latest timestamp in the input. (input: timestamp) |
| `groupBy` | array of string |  | Optional. Treat the input as multiple independent time series, gapfilling each partition separately. |
| `carry` | array of string | conditional {"method":["lastValue"]} | Columns whose value is copied from the most recent real row when `method` is `lastValue`. |

---

## `sort`

Order rows

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* yes

Sorts by one or more keys. Direction defaults to `asc`.

| Field | Type | Required | Description |
|---|---|---|---|
| `by` | array | yes | Ordered list of `{field, dir}` keys. Earlier entries are the primary sort; later entries break ties. |

**Top 10 by ts**

```json
{
  "by": [
    {
      "dir": "desc",
      "field": "ts"
    }
  ],
  "op": "sort"
}
```

---

## `limit`

Cap row count

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* yes

| Field | Type | Required | Description |
|---|---|---|---|
| `n` | integer | yes | Keep at most this many rows. Set to 0 to drop everything. |

---

## `skip`

Drop the first N rows

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* yes

| Field | Type | Required | Description |
|---|---|---|---|
| `n` | integer | yes | Number of rows to discard from the start of the input. Combined with limit gives offset/page semantics. |

---

## `topPerGroup`

Top N rows per partition

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Keeps the highest- (or lowest-) ranked N rows per group. With no partition, equivalent to top N overall.

| Field | Type | Required | Description |
|---|---|---|---|
| `n` | integer | yes | How many rows to keep per partition (1 = top-1). |
| `partitionBy` | array of string |  | Optional. Columns that split the input into partitions. Empty = single partition (top-N overall). |
| `by` | array | yes | Sort keys used to rank rows within each partition. The first key is primary; later keys break ties. |

---

## `groupBy`

Set keys for the next aggregate

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* yes

On its own this is a pass-through. The planner pairs it with the immediately-following `aggregate` op.

| Field | Type | Required | Description |
|---|---|---|---|
| `keys` | array of string | yes | Columns whose unique combinations define each group. The downstream aggregate folds rows within each combination. |

---

## `aggregate`

Fold rows into groups

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* yes

Computes COUNT, SUM, AVG, MIN, MAX over groups defined by a preceding `groupBy` (or the whole stream when standalone).

| Field | Type | Required | Description |
|---|---|---|---|
| `keys` | array of string |  | Optional. Same shape as `groupBy.keys` — when set, the aggregate uses these keys directly without a preceding `groupBy` stage. |
| `aggs` | array | yes | Ordered list of `{fn, field, as}` clauses (e.g. `{fn: "SUM", field: "amount", as: "total"}`). |

---

## `histogram`

Bucket numeric values into equal-width bins

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Replaces input rows with one row per bin: {binStart, binEnd, count}. Provide explicit min/max or let the op derive them.

| Field | Type | Required | Description |
|---|---|---|---|
| `field` | string | yes | Column whose numeric values are bucketed. (input: column) |
| `bins` | integer | yes | Number of equal-width bins to produce. |
| `min` | number |  | Optional lower bound. Defaults to the minimum value seen in the input. |
| `max` | number |  | Optional upper bound. Defaults to the maximum value seen in the input. |
| `asCount` | string |  | Output column for the per-bin count. (input: column-output) Default: `count`. |
| `asStart` | string |  | Output column for each bin's lower edge. (input: column-output) Default: `binStart`. |
| `asEnd` | string |  | Output column for each bin's upper edge. (input: column-output) Default: `binEnd`. |

---

## `window`

Per-row computation within a partition

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

row_number, rank, dense_rank, lag, lead, first_value, last_value. Output preserves input row order.

| Field | Type | Required | Description |
|---|---|---|---|
| `fn` | string | yes | Which windowing function to compute per row. |
| `partitionBy` | array of string |  | Columns that split the input into independent windows. Within each partition, the function is computed in `orderBy` order. |
| `orderBy` | array |  | Sort keys applied within each partition before the function runs. Required for ranking/lag/lead semantics. |
| `field` | string | conditional {"fn":["lag","lead","first_value","last_value"]} | Column the window function reads. Required for `lag`/`lead`/`first_value`/`last_value`; ignored for ranking functions. (input: column) |
| `offset` | integer |  | Row offset for `lag`/`lead`. Defaults to 1 row. |
| `default` | any |  | Value to emit when the window function falls outside the partition (e.g. `lag(1)` on the first row). |
| `as` | string | yes | Column to write the window value into. (input: column-output) |

---

## `lookup`

Hash-join with another dataset

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `classic`

Fetches the right side via a secondary classic query. Use `cacheTtlMs` to avoid re-fetching on every live update.

| Field | Type | Required | Description |
|---|---|---|---|
| `dataset` | string | yes | Dataset name to fetch the join's right side from. (input: dataset) |
| `on` | object | yes | Column names on each side that must equal for a match. |
| `mode` | string |  | `left` keeps every left row; `inner` drops left rows with no match. Default: `left`. |
| `as` | string |  | Optional. When set, the matched right-side row is nested under this column instead of being flattened into the left row. (input: column-output) |
| `select` | array of string |  | Optional. Subset of right-side columns to merge in. Empty means all columns. |
| `where` | any |  | Optional WHERE clause applied to the right-side dataset before the join. |
| `limit` | integer |  | Optional. Cap the number of right-side rows fetched (useful when `dataset` is large). |
| `cacheTtlMs` | integer |  | Cache the right-side fetch for this many milliseconds. 0 disables caching. Useful when the same lookup runs many times under live mode. Default: `0`. |

---

## `asofJoin`

Join on the closest timestamp

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `classic`

For each left row, finds the right row with the closest timestamp on a matching key. Direction backward (default) | forward | nearest, with optional tolerance.

| Field | Type | Required | Description |
|---|---|---|---|
| `dataset` | string | yes | Dataset to fetch the right side from. (input: dataset) |
| `leftTime` | string | yes | Column on the streaming (left) side carrying the row's timestamp. (input: column) |
| `rightTime` | string | yes | Column on the looked-up (right) side carrying the row's timestamp. (input: dataset-column) |
| `leftKey` | string |  | Optional. Column on the left whose value must equal `rightKey` for a match. (input: column) |
| `rightKey` | string |  | Optional. Column on the right whose value must equal `leftKey` for a match. (input: dataset-column) |
| `direction` | string |  | `backward` finds the latest right row at or before the left row; `forward` the earliest at or after; `nearest` the absolute closest. Default: `backward`. |
| `tolerance` | string |  | Optional duration (`"1m"`, `"30s"`). Drops matches further than this from the left row's timestamp. (input: duration) |
| `as` | string |  | Optional. When set, the matched right row is nested under this column. (input: column-output) |
| `select` | array of string |  | Optional subset of right-side columns to merge in. Empty means all columns. |
| `where` | any |  | Optional WHERE clause applied to the right-side dataset before the join. |
| `limit` | integer |  | Optional cap on right-side rows fetched. |

---

## `crossJoin`

Cartesian product with another dataset

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `classic`

Output size is N × M — use sparingly. Right-side rows can be filtered with `where` and capped with `limit`.

| Field | Type | Required | Description |
|---|---|---|---|
| `dataset` | string | yes | Dataset to fetch the cartesian-product right side from. (input: dataset) |
| `as` | string |  | Optional. When set, each right row is nested under this column instead of being flattened into the left row. (input: column-output) |
| `select` | array of string |  | Optional subset of right-side columns to merge in. Empty means all columns. |
| `where` | any |  | Optional WHERE clause applied to the right-side dataset before the cross-join. |
| `limit` | integer |  | Optional cap on right-side rows. Strongly recommended to bound output size. |

---

## `intersect`

Rows present in every source

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `classic`

Set intersection across N sub-pipes, identified by `by` keys (or full-row equality).

| Field | Type | Required | Description |
|---|---|---|---|
| `sources` | array of object | yes | Two or more sub-pipes. Output rows are those present in every sub-pipe (under the chosen identity). |
| `by` | array of string |  | Optional. When set, intersection compares only these columns. Empty falls back to full-row equality. |

---

## `except`

Rows in left but not in right

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `classic`

| Field | Type | Required | Description |
|---|---|---|---|
| `left` | object | yes | Source whose rows are kept when not present in `right`. |
| `right` | object | yes | Source whose rows are subtracted from `left`. |
| `by` | array of string |  | Optional. When set, set difference compares only these columns. Empty falls back to full-row equality. |

---

## `callFunction`

Invoke a registered DTL function

*Live-safe by default:* no &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `functionRegistry`

Calls a function from the function extension. `pure: true` declares the function side-effect-free and makes it live-safe.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Fully qualified DTL function name (e.g. `geo::lookup`). (input: function-name) |
| `mode` | string |  | `perRow` calls the function once per row; `batch` passes the whole row stream to the function once. Default: `perRow`. |
| `pure` | boolean |  | When true, asserts the function has no side effects so live mode can re-run it on each update. Default: `false`. |
| `args` | object |  | Object passed to the function. Values may reference row columns via `$columnName`. |
| `literalArgs` | boolean |  | When true, `$column`-style refs in `args` are passed verbatim instead of resolved against the row. |
| `as` | string |  | Column to store the function result under. (input: column-output) |

**Per-row pure**

```json
{
  "args": {
    "x": "$value"
  },
  "name": "math::abs",
  "op": "callFunction",
  "pure": true
}
```

---

## `callApp`

Invoke a managed app

*Live-safe by default:* no &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `appCaller`

Calls an external app via the runtime extension. Always non-live-safe; live subscriptions require `dryRun: true`.

| Field | Type | Required | Description |
|---|---|---|---|
| `appId` | string | yes | Identifier of the managed app to invoke. (input: app-id) |
| `method` | string |  | App method to call. Defaults to `transform`. Default: `transform`. |
| `capability` | string |  | Capability namespace the app must expose. Defaults to `pipe_query`. Default: `pipe_query`. |
| `batch` | boolean |  | When true, the entire row stream is sent to the app once. When false, the app is invoked per row. Default: `true`. |
| `payload` | object |  | Object merged into the app invocation payload alongside the rows. |
| `dataset` | string |  | Optional dataset name passed to the app for context. (input: dataset) |

---

## `algo`

Run a registered algorithm

*Live-safe by default:* no &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `algorithms`

Invokes a native or external algorithm from the shared catalog (e.g. `minmax_scale`, `kmeans`, `robust_zscore`). `params` are forwarded verbatim to the algorithm — see its descriptor for accepted keys. Live-safety depends on the chosen algorithm: pure ones (most ETL transforms) are live-safe; external/stateful ones are not.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Identifier of a registered algorithm (e.g. `minmax_scale`, `kmeans`). Must exist in the algorithm catalog or the stage fails at build time. |
| `params` | object |  | Algorithm-specific options passed through unchanged. Each algorithm documents its own keys in its catalog descriptor (e.g. `minmax_scale` takes `column` and `as`). |

**Min-max scale a column**

```json
{
  "name": "minmax_scale",
  "op": "algo",
  "params": {
    "as": "v_scaled",
    "column": "v"
  }
}
```

---

## `branch`

Per-row conditional sub-pipes

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no &nbsp;&nbsp; *Requires:* `eval`

Routes each row through `then` or `else` based on a DTL predicate. Live-safety propagates from the children.

| Field | Type | Required | Description |
|---|---|---|---|
| `when` | string | yes | DTL expression evaluated per row. Truthy results take the `then` branch. (input: dtl-expression) |
| `then` | array | yes | Stages applied to rows where the predicate is truthy. |
| `else` | array |  | Optional. Stages applied to rows where the predicate is falsy. Empty drops the row. |

---

## `merge`

Fan-out then concat

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Runs each sub-pipe against a clone of the input and concatenates outputs.

| Field | Type | Required | Description |
|---|---|---|---|
| `sources` | array of object | yes | List of sub-pipes. Each receives a clone of the input rows; outputs are concatenated in order. |

---

## `tap`

Pass-through with a debug label

*Live-safe by default:* yes &nbsp;&nbsp; *Pushable:* no

Records the row count against a label and returns rows unchanged.

| Field | Type | Required | Description |
|---|---|---|---|
| `label` | string |  | Label that surfaces in `stats.pipe[].label` so this checkpoint is identifiable in the response. |

---

