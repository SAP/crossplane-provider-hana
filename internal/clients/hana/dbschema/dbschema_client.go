package dbschema

import (
	"context"
	"fmt"

	"github.com/SAP/crossplane-provider-hana/apis/schema/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

// Client struct holds the connection to the db
type Client struct {
	xsql.DB
}

// New creates a new db client
func New(db xsql.DB) Client {
	return Client{
		DB: db,
	}
}

// Read checks the state of the schema
func (c Client) Read(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) (*v1alpha1.DbSchemaObservation, error) {
	observed := &v1alpha1.DbSchemaObservation{
		SchemaName: "",
		Owner:      "",
	}

	query := "SELECT SCHEMA_NAME, SCHEMA_OWNER FROM SYS.SCHEMAS WHERE SCHEMA_NAME = ?"
	row := c.QueryRowContext(ctx, query, parameters.SchemaName)

	err := row.Scan(&observed.SchemaName, &observed.Owner)
	if xsql.IsNoRows(err) {
		return observed, nil
	}
	return observed, err
}

// Create a new schema
func (c Client) Create(ctx context.Context, parameters *v1alpha1.DbSchemaParameters, args ...any) error {

	query := fmt.Sprintf("CREATE SCHEMA %s", parameters.SchemaName)

	if parameters.Owner != "" {
		query += fmt.Sprintf(" OWNED BY %s", parameters.Owner)
	}

	_, err := c.ExecContext(ctx, query)

	return err
}

// Delete an existing schema
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error {

	query := fmt.Sprintf("DROP SCHEMA %s", parameters.SchemaName)

	_, err := c.ExecContext(ctx, query)

	return err

}
