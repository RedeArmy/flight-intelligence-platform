// Package migrations embeds the SQL migrations so the migrate command and the tests apply exactly what is committed.
//
// Naming: NNNN_description.up.sql and NNNN_description.down.sql, numbered contiguously from 0001. Migrations follow the
// expand/contract rules in migrations/README.md: an up migration only adds, never removes or rewrites.
package migrations

import "embed"

// FS holds every *.sql migration file at its root.
//
//go:embed *.sql
var FS embed.FS
