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
	"github.com/SAP/crossplane-provider-hana/internal/utils"
)

// RoleClient defines the interface for role client operations
type RoleClient interface {
	hana.QueryClient[v1alpha1.RoleParameters, v1alpha1.RoleObservation]
	UpdateLdapGroups(ctx context.Context, parameters *v1alpha1.RoleParameters, groupsToAdd, groupsToRemove []string) error
	UpdatePrivileges(ctx context.Context, parameters *v1alpha1.RoleParameters, privilegesToGrant, privilegesToRevoke []string) error
	UpdateRoles(ctx context.Context, parameters *v1alpha1.RoleParameters, rolesToGrant, rolesToRevoke []string) error
	UpdateRolegroup(ctx context.Context, parameters *v1alpha1.RoleParameters) error
	GetDefaultSchema() string
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
	var rolegroupName sql.NullString
	query := "SELECT ROLE_SCHEMA_NAME, ROLE_NAME, ROLEGROUP_NAME FROM SYS.ROLES WHERE ROLE_NAME = ?"

	var err error
	if err = c.QueryRowContext(ctx, query, parameters.RoleName).Scan(&schema, &observed.RoleName, &rolegroupName); xsql.IsNoRows(err) {
		return observed, nil
	} else if err != nil {
		return observed, err
	}
	observed.Schema = schema.String
	observed.Rolegroup = rolegroupName.String

	if observed.LdapGroups, err = observeLdapGroups(ctx, c.DB, parameters.RoleName); err != nil {
		return observed, err
	}

	grantee := buildGranteeLiteral(parameters.Schema, parameters.RoleName)
	observed.Privileges, err = c.QueryPrivileges(ctx, grantee, privilege.GranteeTypeRole)
	if err != nil {
		return observed, err
	}

	// Roles granted to this role (e.g. HDI container roles) are separate from
	// direct privileges and live in the GRANTED_ROLES catalog view.
	observed.Roles, err = c.QueryRoles(ctx, grantee, privilege.GranteeTypeRole)
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

	query := fmt.Sprintf(`CREATE ROLE %s`, getRoleName(parameters.Schema, parameters.RoleName))

	if len(parameters.LdapGroups) > 0 {
		query += " LDAP GROUP"
		for _, ldapGroup := range parameters.LdapGroups {
			query += fmt.Sprintf(" '%s',", utils.EscapeSingleQuotes(ldapGroup))
		}
		query = strings.TrimSuffix(query, ",")
	}

	if parameters.NoGrantToCreator {
		query += " NO GRANT TO CREATOR"
	}

	if parameters.Rolegroup != "" {
		query += fmt.Sprintf(` SET ROLEGROUP "%s"`, utils.EscapeDoubleQuotes(parameters.Rolegroup))
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

	if len(parameters.Roles) > 0 {
		if err := c.GrantRoles(ctx, c.username, grantee, parameters.Roles); err != nil {
			return fmt.Errorf("failed to grant roles: %w", err)
		}
	}

	return nil
}

// UpdateLdapGroups modifies the ldap groups of an existing role in the db
func (c Client) UpdateLdapGroups(ctx context.Context, parameters *v1alpha1.RoleParameters, groupsToAdd, groupsToRemove []string) error {

	if len(groupsToAdd) > 0 {
		query := fmt.Sprintf(`ALTER ROLE %s ADD LDAP GROUP`, getRoleName(parameters.Schema, parameters.RoleName))
		for _, ldapGroup := range groupsToAdd {
			query += fmt.Sprintf(" '%s',", utils.EscapeSingleQuotes(ldapGroup))
		}
		query = strings.TrimSuffix(query, ",")
		if _, err := c.ExecContext(ctx, query); err != nil {
			return err
		}
	}

	if len(groupsToRemove) > 0 {
		query := fmt.Sprintf("ALTER ROLE %s DROP LDAP GROUP", getRoleName(parameters.Schema, parameters.RoleName))
		for _, ldapGroup := range groupsToRemove {
			query += fmt.Sprintf(" '%s',", utils.EscapeSingleQuotes(ldapGroup))
		}
		query = strings.TrimSuffix(query, ",")
		if _, err := c.ExecContext(ctx, query); err != nil {
			return err
		}
	}

	return nil
}

// GetDefaultSchema returns the default schema for the client
func (c Client) GetDefaultSchema() string {
	return c.username
}

// UpdateRolegroup sets or unsets the rolegroup of an existing role
func (c Client) UpdateRolegroup(ctx context.Context, parameters *v1alpha1.RoleParameters) error {
	roleName := getRoleName(parameters.Schema, parameters.RoleName)
	var query string
	if parameters.Rolegroup != "" {
		query = fmt.Sprintf(`ALTER ROLE %s SET ROLEGROUP "%s"`, roleName, utils.EscapeDoubleQuotes(parameters.Rolegroup))
	} else {
		query = fmt.Sprintf(`ALTER ROLE %s UNSET ROLEGROUP`, roleName)
	}
	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to update rolegroup: %w", err)
	}
	return nil
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

// UpdateRoles grants and/or revokes roles to/from this role. Mirrors
// UpdatePrivileges but delegates to GrantRoles/RevokeRoles, which handle
// schema-qualified role names (e.g. HDI container roles) correctly.
func (c Client) UpdateRoles(ctx context.Context, parameters *v1alpha1.RoleParameters, toGrant, toRevoke []string) error {

	grantee := getRoleName(parameters.Schema, parameters.RoleName)
	if len(toGrant) > 0 {
		if err := c.GrantRoles(ctx, c.username, grantee, toGrant); err != nil {
			return fmt.Errorf("failed to grant roles: %w", err)
		}
	}

	if len(toRevoke) > 0 {
		if err := c.RevokeRoles(ctx, c.username, grantee, toRevoke); err != nil {
			return fmt.Errorf("failed to revoke roles: %w", err)
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

// getRoleName builds a quoted SQL identifier for use in DDL/DCL statements
// (CREATE ROLE, GRANT ... TO <grantee>, etc.), where the role name must be a
// quoted identifier: "NAME" or "SCHEMA"."NAME".
func getRoleName(schemaName, roleName string) string {
	return joinRoleIdentifier(schemaName, roleName, true)
}

// buildGranteeLiteral builds the grantee value used to match the GRANTEE column
// of the GRANTED_PRIVILEGES / GRANTED_ROLES catalog views. Those columns store
// the raw, UNQUOTED identifier value (e.g. DUMMY_SCHEMA::dummy_role_g), so the
// grantee passed to QueryPrivileges/QueryRoles must be unquoted — unlike
// getRoleName, which quotes for use as a SQL identifier. When a schema is set,
// the two are joined by a bare dot (schema.name) so addGranteeQuery can split
// them into the GRANTEE and GRANTEE_SCHEMA_NAME predicates.
func buildGranteeLiteral(schemaName, roleName string) string {
	return joinRoleIdentifier(schemaName, roleName, false)
}

// joinRoleIdentifier joins an optional schema and a role name using HANA's
// schema-qualification structure (schema and name are separate components joined
// by a dot). When quoted is true each component is wrapped in double quotes for
// use as a SQL identifier ("SCHEMA"."NAME"); when false the raw values are joined
// (schema.name) to match a catalog column literal. The dot always sits between
// the two components, never inside a quoted identifier — HANA treats "A.B" as a
// single identifier but "A"."B" (or a.b) as schema-qualified.
func joinRoleIdentifier(schemaName, roleName string, quoted bool) string {
	name := roleName
	if quoted {
		name = fmt.Sprintf(`"%s"`, utils.EscapeDoubleQuotes(roleName))
	}
	if schemaName == "" {
		return name
	}
	schema := schemaName
	if quoted {
		schema = fmt.Sprintf(`"%s"`, utils.EscapeDoubleQuotes(schemaName))
	}
	return fmt.Sprintf("%s.%s", schema, name)
}
