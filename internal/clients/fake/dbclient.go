package fake

import (
	"context"
	"database/sql"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
)

type MockDB struct {
	MockExecContext          func(ctx context.Context, query string, args ...any) (sql.Result, error)
	MockQueryRowContext      func(ctx context.Context, query string, args ...any) *sql.Row
	MockQueryContext         func(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	MockGetConnectionDetails func(username, password string) managed.ConnectionDetails
	MockConnect              func(ctx context.Context, creds map[string][]byte) error
}

func (m MockDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return m.MockExecContext(ctx, query, args...)
}
func (m MockDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return m.MockQueryRowContext(ctx, query, args...)
}
func (m MockDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if m.MockQueryContext != nil {
		return m.MockQueryContext(ctx, query, args...)
	}
	// Return empty result set by default
	return MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
}
func (m MockDB) GetConnectionDetails(username, password string) managed.ConnectionDetails {
	return m.MockGetConnectionDetails(username, password)
}
func (m MockDB) Connect(ctx context.Context, creds map[string][]byte) error {
	return m.MockConnect(ctx, creds)
}

func (m MockDB) Disconnect() error {
	return nil
}

// nolint: contextcheck
func MockRowsToSQLRows(mockRows *sqlmock.Rows) *sql.Rows {
	db, mock, _ := sqlmock.New()
	mock.ExpectQuery("select").WillReturnRows(mockRows)
	rows, err := db.QueryContext(context.Background(), "select")
	if err != nil {
		println("%v", err)
		return nil
	}
	return rows
}
