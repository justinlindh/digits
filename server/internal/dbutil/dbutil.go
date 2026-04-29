// Package dbutil provides shared helpers for database query construction.
package dbutil

import (
	"fmt"
	"strings"
)

// Placeholders returns a comma-separated string of PostgreSQL positional
// placeholders ($1, $2, …, $n) for use in SQL IN clauses.
func Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(parts, ", ")
}
