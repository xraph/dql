// Package exec runs a planned query against a SQL database and assembles the
// rows the processor then finishes in memory.
//
// The database is reached through SQLQuerier, which is deliberately the shape
// database/sql already has. Nothing here is invented: *sql.DB satisfies it
// through a four-line adapter, and so does any pooled or instrumented wrapper
// a caller has already built. A driver-specific interface would have made the
// library usable only by whoever wrote it.
package exec

import "context"

// SQLQuerier is any database/sql-shaped connection.
type SQLQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (SQLRows, error)
}

// SQLRows is the cursor SQLQuerier returns. The method set matches *sql.Rows,
// minus the parts a query engine never needs.
type SQLRows interface {
	// Close releases the cursor. Callers always call it, including on the
	// error paths.
	Close() error

	// Columns names the result columns, in order. Rows are assembled by
	// zipping these against each scanned row, so the count must match what
	// Scan expects.
	Columns() ([]string, error)

	// Next advances to the next row, reporting whether there is one.
	Next() bool

	// Scan copies the current row into dest, which the engine supplies as
	// pointers to any.
	Scan(dest ...any) error

	// Err reports an error that ended iteration early, as distinct from a
	// clean end of rows. Ignoring it silently truncates results.
	Err() error
}
