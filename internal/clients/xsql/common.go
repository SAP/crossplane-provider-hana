package xsql

import (
	"context"
	"database/sql"
	"errors"
)

// DB is the query interface used by clients. Close releases the caller's handle;
// connectors may keep the underlying shared pool alive for reuse.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	Close() error
}

// Connector manages shared DB pools keyed by credentials. Callers must close the
// DB returned by Connect when their external operation finishes.
type Connector interface {
	Connect(ctx context.Context, creds map[string][]byte) (DB, error)
	Disconnect() error
}

// IsNoRows returns true if the supplied error indicates no rows were returned.
func IsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
