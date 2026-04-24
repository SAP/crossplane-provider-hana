package xsql

import (
	"context"
	"database/sql"
	"errors"
)

// DB is the query interface satisfied by *sql.DB and used by clients.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Connector manages a pool of DB connections keyed by credentials.
type Connector interface {
	Connect(ctx context.Context, creds map[string][]byte) (DB, error)
	Disconnect() error
}

// IsNoRows returns true if the supplied error indicates no rows were returned.
func IsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
