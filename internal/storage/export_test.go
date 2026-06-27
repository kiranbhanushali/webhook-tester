package storage

import "database/sql"

// DB exposes the underlying *sql.DB for white-box pragma verification in tests.
// This file is compiled only during test runs (it is a _test.go file).
func (s *SQLite) DB() *sql.DB { return s.db }
