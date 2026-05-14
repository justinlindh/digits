package tracing

import (
	"database/sql"
	"fmt"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

// OpenSQLDB opens a *sql.DB through otelsql so every query produces a
// child span on the surrounding HTTP request span. The driver name is
// fixed to "postgres" (lib/pq) because that's the only driver we use;
// passing a different name is a sign of a bug at the call site, not a
// configuration knob.
//
// Privacy: otelsql is configured with DisableQuery=true, which suppresses
// the db.statement span attribute. The default behavior would echo the
// raw SQL text into every span, and our query patterns include things
// like SELECT ... FROM users WHERE email = $1. Even with placeholders,
// the surrounding query shape can make a phone-number table read
// identifiable. We disable the field outright; a bound parameter
// inspected by an operator viewing a Tempo trace is not the privacy
// surface we want to ship. Operators who need to debug a slow query can
// run EXPLAIN against the database directly.
//
// What otelsql still records:
//
//   - db.system: "postgresql"
//   - db.name (the database identifier, e.g. "digits")
//   - the operation kind (db.connection.prepare, db.connection.query,
//     etc.) plus duration
//   - error attribute on failed calls
//
// None of those carry user identifiers.
func OpenSQLDB(driverName, dataSourceName string, dbName string) (*sql.DB, error) {
	if driverName == "" {
		driverName = "postgres"
	}
	db, err := otelsql.Open(driverName, dataSourceName,
		otelsql.WithAttributes(
			semconv.DBSystemPostgreSQL,
			attribute.String("db.name", dbName),
		),
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			// DisableQuery suppresses the db.statement attribute so
			// query text never reaches a span. See package doc above.
			DisableQuery: true,
			// OmitConnResetSession: connection-pool churn spans add
			// noise without diagnostic value at our query rate.
			OmitConnResetSession: true,
			// OmitConnPrepare: lib/pq prepares every statement; per-
			// prepare spans triple the span count without information
			// the surrounding query span doesn't already carry.
			OmitConnPrepare: true,
			// RowsNext: leaving false (default) so we don't emit one
			// span event per fetched row. Row-level events would dwarf
			// the request span and could leak result-set cardinality
			// information that an aggregated query span obscures.
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("otelsql open %s: %w", driverName, err)
	}
	return db, nil
}
