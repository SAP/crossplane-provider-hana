package dbschema

import (
	"context"
	"fmt"

	"github.com/SAP/crossplane-provider-hana/apis/schema/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/utils"
)

// DbSchemaClient defines the interface for dbschema client operations
type DbSchemaClient = hana.QueryClient[v1alpha1.DbSchemaParameters, v1alpha1.DbSchemaObservation]

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
	err := c.QueryRowContext(ctx, query, parameters.SchemaName).Scan(&observed.SchemaName, &observed.Owner)
	if xsql.IsNoRows(err) {
		return observed, nil
	}

	return observed, err
}

// Create a new schema
func (c Client) Create(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error {

	query := fmt.Sprintf(`CREATE SCHEMA "%s"`, utils.EscapeDoubleQuotes(parameters.SchemaName))

	if parameters.Owner != "" {
		query += fmt.Sprintf(" OWNED BY %s", parameters.Owner)
	}

	_, err := c.ExecContext(ctx, query)

	return err
}

// Delete an existing schema
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error {

	query := fmt.Sprintf(`DROP SCHEMA "%s"`, utils.EscapeDoubleQuotes(parameters.SchemaName))

	_, err := c.ExecContext(ctx, query)

	return err

}
