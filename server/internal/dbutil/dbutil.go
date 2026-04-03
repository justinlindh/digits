// Package dbutil provides shared helpers for database query construction.
package dbutil

import (
	"fmt"
	"strings"
)

// Placeholders returns a comma-separated string of PostgreSQL positional
// placeholders ($1, $2, …, $n) for use in SQL IN clauses.
// offset shifts the starting index (e.g. offset=2 gives $2,$3,…).
func Placeholders(n, offset int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", offset+i+1)
	}
	return strings.Join(parts, ", ")
}
