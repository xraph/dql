# DQL — Data Query Language

A declarative query language for Go. You describe *what* you want as a
document; DQL plans it, pushes down what the database can do, and finishes the
rest in memory.

```yaml
from:
  dataset: spaces
where:
  field: parent_id
  op: "=="
  value: "$parentId"
orderBy:
  - field: sort_order
    dir: asc
```

Plus a pipe mode — an ordered chain of operators applied to a stream of rows:

```yaml
from:
  dataset: events
pipe:
  - op: filter
    where: { field: status, op: "==", value: "open" }
  - op: groupBy
    keys: [assignee]
  - op: aggregate
    aggs: [{ fn: count, as: total }]
  - op: sort
    by: [{ field: total, dir: desc }]
  - op: limit
    n: 10
```

> **On the name.** DQL is *Data Query Language*. It queries whatever data a
> host exposes and assumes no domain of its own.

## Install

```bash
go get github.com/xraph/dql
```

## What's in the box

| Package | Purpose |
|---------|---------|
| `dsl` | The document types — `QueryDSL`, clauses, plan types |
| `parser` | Parse and validate a document, classic or pipe mode |
| `planner` | Decide what pushes down to SQL and what does not |
| `sqlgen` | Emit SQL and its arguments from a plan |
| `processor` | Finish in memory: computed columns, expression filters, sort |
| `pipe` | The operator library — reshape, textual, quality, time, set ops |
| `exec` | Run a plan against a `database/sql`-shaped connection |
| `expand` | Turn id columns into display fields |
| `scope` | Partition/tenant scoping (see below) |

## Operators

The pipe catalog defines 39 operators — filter, project, aggregate, window,
joins, time bucketing, reshaping, quality checks, set operations. They ship
with the language rather than being left to the host: the operator set *is*
the language, and a query that runs against one host should mean the same
thing against another.

**[Operator reference →](docs/OPERATORS.md)** — every operator with its config
schema, examples, and requirements. Generated from the catalog, so it cannot
drift from the code.

Most operators are self-contained. A few need something from the host, and say
so rather than leaving you to find out at query time:

```go
octx := &pipe.OpContext{Eval: myEvaluator}

for name, needs := range pipe.MissingRequirements(octx) {
	log.Printf("operator %s unavailable: needs %v", name, needs)
}
```

Pass the same `OpContext` to a completion request and stages the deployment
cannot run are left out — an editor should not suggest `callApp` to a host with
no app caller:

```go
items := pipe.CompleteText(text, cursor, pipe.CompletionContext{Services: octx})
```

Leaving `Services` nil means "not known", not "nothing wired", so an editor
working on a file with no host attached still sees the whole language.

## Bring your own database

`exec.SQLQuerier` is modelled on `database/sql`, so anything shaped like it fits
— including a pooled or instrumented wrapper:

```go
type SQLQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (SQLRows, error)
}
```

`*sql.DB` does not satisfy it directly: the standard library puts the context on
`QueryContext`, and returns `*sql.Rows` where this returns an interface. Adapt
it in five lines — `*sql.Rows` already satisfies `SQLRows`, so only the call
needs wrapping:

```go
type sqlDB struct{ *sql.DB }

func (d sqlDB) Query(ctx context.Context, q string, args ...any) (exec.SQLRows, error) {
	return d.DB.QueryContext(ctx, q, args...)
}
```

## Partition scoping

Multi-tenant callers need every query confined to a tenant, and getting that
wrong is a data leak rather than a bug. DQL does not guess what partitions your
data — you declare it, and the planner and generator apply it to base tables
and joins:

```go
sc := scope.Scope{
	{Name: "tenant_id", Value: tenantID, Required: true, ScopeJoins: true},
	{Name: "project_id", Value: projectID},
}
```

`Required` emits the predicate even when a table does not declare the column.
`ScopeJoins` also scopes joined tables, in the `ON` clause rather than `WHERE`,
so an out-of-scope row fails the join instead of NULL-padding through a LEFT
join.

A **nil** scope is refused. An explicitly empty one — `scope.Scope{}` — is
honoured. Those are different intentions, and only one of them is safe to guess
at: a caller who forgot would otherwise get SQL spanning every tenant, quietly.

## Expressions

Computed columns and expression filters are evaluated through an interface, so
the expression language is yours to choose:

```go
type ExprEvaluator interface {
	Eval(ctx context.Context, expr string, row map[string]any) (any, error)
}
```

[github.com/xraph/dtl](https://github.com/xraph/dtl) satisfies it directly.

## Status

DQL has been in production use as an embedded query engine before being
published here as a standalone project. The document format is stable.

## Editor support

Highlighting and language intelligence both ship with the language, so an
editor needs no bespoke client code:

| Want | Use |
|------|-----|
| Syntax highlighting | [`syntaxes/`](syntaxes/) — TextMate grammar, scope `source.dql` |
| Completion, hover, diagnostics | [`cmd/dql-lsp`](cmd/dql-lsp) — a Language Server Protocol server |
| To build your own | [`lang`](lang/) — the same features as plain functions |

```bash
go install github.com/xraph/dql/cmd/dql-lsp@latest
```

The server works on a file on disk with nothing else running. A host that knows
more — which datasets exist, which functions are registered — passes that in and
gets richer completions; without it, the language itself is still there.

## License

Apache License 2.0 — see [LICENSE](LICENSE), [NOTICE](NOTICE), and
[TRADEMARKS](TRADEMARKS).
