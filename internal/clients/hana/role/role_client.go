package role

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/privilege"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

// RoleClient defines the interface for role client operations
type RoleClient interface {
	hana.QueryClient[v1alpha1.RoleParameters, v1alpha1.RoleObservation]
	UpdateLdapGroups(ctx context.Context, parameters *v1alpha1.RoleParameters, groupsToAdd, groupsToRemove []string) error
	UpdatePrivileges(ctx context.Context, parameters *v1alpha1.RoleParameters, privilegesToGrant, privilegesToRevoke []string) error
}

// Client struct holds the connection to the db
type Client struct {
	xsql.DB
	privilege.Client
	username string
}

// New creates a new db client
func New(db xsql.DB, username string) Client {
	return Client{
		DB:       db,
		Client:   &privilege.PrivilegeClient{DB: db},
		username: username,
	}
}

// Observe checks the state of the role
func (c Client) Read(ctx context.Context, parameters *v1alpha1.RoleParameters) (*v1alpha1.RoleObservation, error) {

	observed := &v1alpha1.RoleObservation{
		RoleName:   "",
		Schema:     "",
		Privileges: nil,
		LdapGroups: nil,
	}

	var schema sql.NullString
	query := "SELECT ROLE_SCHEMA_NAME, ROLE_NAME FROM SYS.ROLES WHERE ROLE_NAME = ?"

	var err error
	if err = c.QueryRowContext(ctx, query, parameters.RoleName).Scan(&schema, &observed.RoleName); xsql.IsNoRows(err) {
		return observed, nil
	} else if err != nil {
		return observed, err
	}
	observed.Schema = schema.String

	if observed.LdapGroups, err = observeLdapGroups(ctx, c.DB, parameters.RoleName); err != nil {
		return observed, err
	}

	grantee := getRoleName(parameters.Schema, parameters.RoleName)
	observed.Privileges, err = c.QueryPrivileges(ctx, grantee, privilege.GranteeTypeRole)
	if err != nil {
		return observed, err
	}

	return observed, nil
}

func observeLdapGroups(ctx context.Context, db xsql.DB, roleName string) (ldapGroups []string, errr error) {
	queryLdapGroups := "SELECT ROLE_NAME, LDAP_GROUP_NAME FROM SYS.ROLE_LDAP_GROUPS WHERE ROLE_NAME = ?"
	ldapRows, err := db.QueryContext(ctx, queryLdapGroups, roleName)
	if err != nil {
		return nil, err
	}
	defer ldapRows.Close() //nolint:errcheck
	for ldapRows.Next() {
		var role, ldapGroup string
		rowErr := ldapRows.Scan(&role, &ldapGroup)
		if rowErr == nil {
			ldapGroups = append(ldapGroups, ldapGroup)
		}
	}
	if err := ldapRows.Err(); err != nil {
		return nil, err
	}
	return ldapGroups, nil
}

// Create creates a new role in the db
func (c Client) Create(ctx context.Context, parameters *v1alpha1.RoleParameters) error {

	query := fmt.Sprintf("CREATE ROLE %s", getRoleName(parameters.Schema, parameters.RoleName))

	if len(parameters.LdapGroups) > 0 {
		query += " LDAP GROUP"
		for _, ldapGroup := range parameters.LdapGroups {
			query += fmt.Sprintf(" '%s',", ldapGroup)
		}
		query = strings.TrimSuffix(query, ",")
	}

	if parameters.NoGrantToCreator {
		query += " NO GRANT TO CREATOR"
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return err
	}

	grantee := getRoleName(parameters.Schema, parameters.RoleName)
	if len(parameters.Privileges) > 0 {
		if err := c.GrantPrivileges(ctx, c.username, grantee, parameters.Privileges); err != nil {
			return fmt.Errorf("failed to grant privileges: %w", err)
		}
	}

	return nil
}

// UpdateLdapGroups modifies the ldap groups of an existing role in the db
func (c Client) UpdateLdapGroups(ctx context.Context, parameters *v1alpha1.RoleParameters, groupsToAdd, groupsToRemove []string) error {

	if len(groupsToAdd) > 0 {
		query := fmt.Sprintf("ALTER ROLE %s ADD LDAP GROUP", getRoleName(parameters.Schema, parameters.RoleName))
		for _, ldapGroup := range groupsToAdd {
			query += fmt.Sprintf(" '%s',", ldapGroup)
		}
		query = strings.TrimSuffix(query, ",")
		if _, err := c.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to add ldap groups: %w", err)
		}
	}

	if len(groupsToRemove) > 0 {
		query := fmt.Sprintf("ALTER ROLE %s DROP LDAP GROUP", getRoleName(parameters.Schema, parameters.RoleName))
		for _, ldapGroup := range groupsToRemove {
			query += fmt.Sprintf(" '%s',", ldapGroup)
		}
		query = strings.TrimSuffix(query, ",")
		if _, err := c.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to remove ldap groups: %w", err)
		}
	}

	return nil
}

// GetDefaultSchema returns the default schema for the client
func (c Client) GetDefaultSchema() string {
	return c.username
}

// UpdatePrivileges modifies the privileges of an existing role in the db
func (c Client) UpdatePrivileges(ctx context.Context, parameters *v1alpha1.RoleParameters, toGrant, toRevoke []string) error {

	grantee := getRoleName(parameters.Schema, parameters.RoleName)
	if len(toGrant) > 0 {
		if err := c.GrantPrivileges(ctx, c.username, grantee, toGrant); err != nil {
			return fmt.Errorf("failed to grant privileges: %w", err)
		}
	}

	if len(toRevoke) > 0 {
		if err := c.RevokePrivileges(ctx, c.username, grantee, toRevoke); err != nil {
			return fmt.Errorf("failed to revoke privileges: %w", err)
		}
	}

	return nil
}

// Delete removes an existing role from the db
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.RoleParameters) error {

	query := fmt.Sprintf("DROP ROLE %s", getRoleName(parameters.Schema, parameters.RoleName))

	if _, err := c.ExecContext(ctx, query); err != nil {
		return err
	}

	return nil
}

func getRoleName(schemaName, roleName string) string {
	if schemaName != "" {
		return fmt.Sprintf("%s.%s", schemaName, roleName)
	}
	return roleName
}
