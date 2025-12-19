package xsql

import (
	"context"
	"errors"

	"database/sql"

	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
)

// A DB client.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	GetConnectionDetails(username, password string) managed.ConnectionDetails
	Connect(ctx context.Context, creds map[string][]byte) error
	Disconnect() error
}

// IsNoRows returns true if the supplied error indicates no rows were returned.
func IsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
