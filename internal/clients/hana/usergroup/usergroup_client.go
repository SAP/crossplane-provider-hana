package usergroup

import (
	"context"
	"fmt"
	"strings"

	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/internal/clients/xsql"
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

// Observe checks the state of the usergroup
func (c Client) Read(ctx context.Context, parameters *v1alpha1.UsergroupParameters) (*v1alpha1.UsergroupObservation, error) {

	observed := &v1alpha1.UsergroupObservation{
		UsergroupName:    "",
		DisableUserAdmin: false,
		Parameters:       make(map[string]string),
	}

	var disableUserAdminString string
	query := "SELECT USERGROUP_NAME, IS_USER_ADMIN_ENABLED FROM SYS.USERGROUPS WHERE USERGROUP_NAME = ?"
	if err := c.QueryRowContext(ctx, query, parameters.UsergroupName).Scan(&observed.UsergroupName, &disableUserAdminString); xsql.IsNoRows(err) {
		return observed, nil
	} else if err != nil {
		return observed, err
	}
	if disableUserAdminString == "FALSE" {
		observed.DisableUserAdmin = true
	}

	queryParams := "SELECT USERGROUP_NAME, PARAMETER_NAME, PARAMETER_VALUE FROM SYS.USERGROUP_PARAMETERS WHERE USERGROUP_NAME = ?"
	paramRows, err := c.QueryContext(ctx, queryParams, parameters.UsergroupName)
	if xsql.IsNoRows(err) {
		return observed, nil
	} else if err != nil {
		return observed, err
	}
	defer paramRows.Close() //nolint:errcheck

	for paramRows.Next() {
		var name, parameter, value string
		rowErr := paramRows.Scan(&name, &parameter, &value)
		if rowErr == nil {
			observed.Parameters[parameter] = value
		}
	}

	return observed, paramRows.Err()
}

// Create creates a usergroup
func (c Client) Create(ctx context.Context, parameters *v1alpha1.UsergroupParameters, args ...any) error {

	query := fmt.Sprintf("CREATE USERGROUP %s", parameters.UsergroupName)

	if parameters.DisableUserAdmin {
		query += " DISABLE USER ADMIN"
	}

	if parameters.NoGrantToCreator {
		query += " NO GRANT TO CREATOR"
	}

	if len(parameters.Parameters) > 0 {
		query += " SET PARAMETER"
		for key, value := range parameters.Parameters {
			query += fmt.Sprintf(" '%s' = '%s',", key, value)
		}
		query = strings.TrimSuffix(query, ",")
	}

	if parameters.EnableParameterSet != "" {
		query += fmt.Sprintf(" ENABLE PARAMETER SET '%s'", parameters.EnableParameterSet)
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return err
	}

	return nil
}

// UpdateDisableUserAdmin updates the disableUserAdmin property of the usergroup
func (c Client) UpdateDisableUserAdmin(ctx context.Context, parameters *v1alpha1.UsergroupParameters) error {

	query := fmt.Sprintf("ALTER USERGROUP %s", parameters.UsergroupName)

	if parameters.DisableUserAdmin {
		query += " DISABLE USER ADMIN"
	} else {
		query += " ENABLE USER ADMIN"
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to update disable user admin: %w", err)
	}

	return nil
}

// UpdateParameters updates the parameters of the usergroup
func (c Client) UpdateParameters(ctx context.Context, parameters *v1alpha1.UsergroupParameters, changedParameters map[string]string) error {

	query := fmt.Sprintf("ALTER USERGROUP %s", parameters.UsergroupName)
	query += " SET PARAMETER"
	for key, value := range changedParameters {
		query += fmt.Sprintf(" '%s' = '%s',", key, value)
	}
	query = strings.TrimSuffix(query, ",")
	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to update parameters: %w", err)
	}

	return nil
}

// Delete deletes the usergroup
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.UsergroupParameters) error {

	query := fmt.Sprintf("DROP USERGROUP %s", parameters.UsergroupName)

	if _, err := c.ExecContext(ctx, query); err != nil {
		return err
	}

	return nil
}
