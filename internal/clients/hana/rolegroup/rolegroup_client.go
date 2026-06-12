package rolegroup

import (
	"context"
	"fmt"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/utils"
)

// RolegroupClient defines the interface for rolegroup client operations
type RolegroupClient interface {
	hana.QueryClient[v1alpha1.RolegroupParameters, v1alpha1.RolegroupObservation]
	UpdateDisableRoleAdmin(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error
}

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

// Read checks the state of the rolegroup
func (c Client) Read(ctx context.Context, parameters *v1alpha1.RolegroupParameters) (*v1alpha1.RolegroupObservation, error) {

	observed := &v1alpha1.RolegroupObservation{
		RolegroupName:    "",
		DisableRoleAdmin: false,
	}

	var isRoleAdminEnabled string
	query := "SELECT ROLEGROUP_NAME, IS_ROLE_ADMIN_ENABLED FROM SYS.ROLEGROUPS WHERE ROLEGROUP_NAME = ?"
	if err := c.QueryRowContext(ctx, query, parameters.RolegroupName).Scan(&observed.RolegroupName, &isRoleAdminEnabled); xsql.IsNoRows(err) {
		return observed, nil
	} else if err != nil {
		return observed, err
	}
	if isRoleAdminEnabled == "FALSE" {
		observed.DisableRoleAdmin = true
	}

	return observed, nil
}

// Create creates a rolegroup
func (c Client) Create(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {

	query := fmt.Sprintf(`CREATE ROLEGROUP "%s"`, utils.EscapeDoubleQuotes(parameters.RolegroupName))

	if parameters.ForGrantsOnTenantObjects {
		query += " FOR GRANTS ON TENANT OBJECTS"
	}

	if parameters.NoGrantToCreator {
		// NO GRANT TO CREATOR cannot be combined with ENABLE ROLE ADMIN
		query += " NO GRANT TO CREATOR"
	} else if !parameters.DisableRoleAdmin {
		// HANA default is role admin disabled; only emit ENABLE when explicitly wanted.
		// DISABLE ROLE ADMIN is not valid in CREATE — use ALTER after creation instead.
		query += " ENABLE ROLE ADMIN"
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return err
	}

	return nil
}

// UpdateDisableRoleAdmin updates the disableRoleAdmin property of the rolegroup
func (c Client) UpdateDisableRoleAdmin(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {

	query := fmt.Sprintf(`ALTER ROLEGROUP "%s"`, utils.EscapeDoubleQuotes(parameters.RolegroupName))

	if parameters.DisableRoleAdmin {
		query += " DISABLE ROLE ADMIN"
	} else {
		query += " ENABLE ROLE ADMIN"
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to update disable role admin: %w", err)
	}

	return nil
}

// Delete deletes the rolegroup
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {

	query := fmt.Sprintf(`DROP ROLEGROUP "%s"`, utils.EscapeDoubleQuotes(parameters.RolegroupName))

	if _, err := c.ExecContext(ctx, query); err != nil {
		return err
	}

	return nil
}
