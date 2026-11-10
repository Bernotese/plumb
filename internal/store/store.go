// Package store is the only package that talks to SQLite
// (docs/ARCHITECTURE.md §2, D-020). This file has only the embedded
// migrations; the behaviour comes with later cards.
package store

import "embed"

// Migrations holds the forward-only migration files, named
// migrations/NNNN_name.sql. They run in file-name order, each exactly once.
//
//go:embed migrations/*.sql
var Migrations embed.FS
