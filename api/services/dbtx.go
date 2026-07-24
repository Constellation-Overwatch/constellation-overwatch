package services

import (
	"database/sql"
)

// DBTX is the common query surface implemented by both *sql.DB and *sql.Tx.
// It lets security-sensitive bootstrap work commit user and invite state as
// one transaction.
type DBTX interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}
