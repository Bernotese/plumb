// Package store is the only package that talks to SQLite
// (docs/ARCHITECTURE.md §2, D-020). This file has only the embedded
// migrations; the behaviour comes with later cards.
package store

import "embed"

// Rules for the code that opens the database (T-004):
//   - Run "PRAGMA foreign_keys = ON" on every connection. SQLite has it off
//     by default, and the schema relies on it.
//   - Use WAL mode and a busy timeout.
//
// Migrations holds the forward-only migration files, named
// migrations/NNNN_name.sql. They run in file-name order, each exactly once.
//
//go:embed migrations/*.sql
var Migrations embed.FS
