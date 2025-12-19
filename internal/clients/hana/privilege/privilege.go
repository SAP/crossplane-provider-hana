package privilege

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

const (
	errUnknownPrivilege                 = "unknown type of privilege: %s"
	errParsePrivilege                   = "failed to parse privilege %s: %w"
	ErrUnknownPrivilegeManagementPolicy = "unknown privilege management policy: %s"
	ErrObservationNil                   = "observed user observation cannot be nil"
)

type DefaultSchema = string
type Grantee = string
type GranteeType string

const (
	GranteeTypeUser GranteeType = "USER"
	GranteeTypeRole GranteeType = "ROLE"
)

type Client interface {
	GrantPrivileges(context.Context, DefaultSchema, Grantee, []string) error
	GrantRoles(context.Context, DefaultSchema, Grantee, []string) error
	RevokePrivileges(context.Context, DefaultSchema, Grantee, []string) error
	RevokeRoles(context.Context, DefaultSchema, Grantee, []string) error
	QueryPrivileges(context.Context, Grantee, GranteeType) ([]string, error)
	QueryRoles(context.Context, Grantee, GranteeType) ([]string, error)
}

type PrivilegeClient struct {
	xsql.DB
}

// GrantPrivileges TODO: Support WITH GRANT OPTION and WITH ADMIN OPTION clauses: eg: GRANT USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek TO User1 WITH GRANT OPTION;
func (c *PrivilegeClient) GrantPrivileges(ctx context.Context, grantor DefaultSchema, grantee Grantee, privilegeStrings []string) error {
	groupedObjects, err := groupPrivilegesByType(privilegeStrings, grantor)
	if err != nil {
		return err
	}

	for _, group := range groupedObjects {
		query := "GRANT " + group + " TO " + grantee
		if _, err := c.ExecContext(ctx, query); err != nil {
			return err
		}
	}

	return nil
}

func (c *PrivilegeClient) GrantRoles(ctx context.Context, grantor DefaultSchema, grantee Grantee, roleNames []string) error {
	if len(roleNames) == 0 {
		return nil
	}

	rolesStr := strings.Join(roleNames, ", ")
	query := "GRANT " + rolesStr + " TO " + grantee
	_, err := c.ExecContext(ctx, query)
	return err
}

func (c *PrivilegeClient) RevokePrivileges(ctx context.Context, defaultSchema DefaultSchema, grantee Grantee, privilegeStrings []string) error {
	groupedObjects, err := groupPrivilegesByType(privilegeStrings, defaultSchema)
	if err != nil {
		return err
	}

	for _, group := range groupedObjects {
		query := "REVOKE " + group + " FROM " + grantee
		if _, err := c.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func (c *PrivilegeClient) RevokeRoles(ctx context.Context, defaultSchema DefaultSchema, grantee Grantee, roleNames []string) error {
	if len(roleNames) == 0 {
		return nil
	}

	rolesStr := strings.Join(roleNames, ", ")
	query := "REVOKE " + rolesStr + " FROM " + grantee
	_, err := c.ExecContext(ctx, query)
	return err
}

func addGranteeQuery(query string, queryArgs []any, grantee string, granteeType GranteeType) (string, []any) {
	query += " AND GRANTEE = ?"
	if parts := strings.SplitN(grantee, ".", 2); len(parts) == 2 {
		query += " AND GRANTEE_SCHEMA_NAME = ?"
		queryArgs = append(queryArgs, parts[1], parts[0])
	} else {
		queryArgs = append(queryArgs, grantee)
	}
	return query, queryArgs
}

// QueryPrivileges TODO: Test to query CLIENTSIDE ENCRYPTION COLUMN KEY and STRUCTURED PRIVILEGE types in HANA instance
// Reference: https://help.sap.com/docs/SAP_HANA_PLATFORM/4fe29514fd584807ac9f2a04f6754767/20f674e1751910148a8b990d33efbdc5.html?locale=en-US
func (c *PrivilegeClient) QueryPrivileges(ctx context.Context, grantee Grantee, granteeType GranteeType) ([]string, error) {
	observed := []string{}
	query := "SELECT OBJECT_TYPE, PRIVILEGE, SCHEMA_NAME, OBJECT_NAME FROM GRANTED_PRIVILEGES WHERE GRANTEE_TYPE = ?"
	queryArgs := []any{granteeType}
	query, queryArgs = addGranteeQuery(query, queryArgs, grantee, granteeType)

	privRows, err := c.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return observed, err
	}
	defer privRows.Close() //nolint:errcheck
	if xsql.IsNoRows(err) {
		return observed, nil
	}
	for privRows.Next() {
		var objectType, privilege string
		var schemaName, objectName sql.NullString
		rowErr := privRows.Scan(&objectType, &privilege, &schemaName, &objectName)
		if rowErr != nil {
			return observed, rowErr
		}
		switch objectType {
		case "SYSTEMPRIVILEGE":
		case "SCHEMA":
			privilege = fmt.Sprintf("%s ON SCHEMA %s", privilege, schemaName.String)
		case "SOURCE":
			privilege = fmt.Sprintf("%s ON REMOTE SOURCE %s", privilege, objectName.String)
		case "USERGROUP":
			privilege = fmt.Sprintf("USERGROUP %s ON USERGROUP %s", privilege, objectName.String)
		case "CLIENTSIDE ENCRYPTION COLUMN KEY":
			privilege = fmt.Sprintf("%s ON CLIENTSIDE ENCRYPTION COLUMN KEY %s", privilege, objectName.String)
		case "STRUCTURED_PRIVILEGE":
			privilege = fmt.Sprintf("STRUCTURED PRIVILEGE %s", objectName.String)
		default:
			privilege = fmt.Sprintf("%s ON %s.%s", privilege, schemaName.String, objectName.String)
		}
		observed = append(observed, privilege)
	}
	if err := privRows.Err(); err != nil {
		return observed, err
	}
	return observed, nil
}

func (c *PrivilegeClient) QueryRoles(ctx context.Context, grantee Grantee, granteeType GranteeType) ([]string, error) {
	observed := []string{}
	query := "SELECT ROLE_SCHEMA_NAME, ROLE_NAME FROM GRANTED_ROLES WHERE GRANTEE_TYPE = ?"
	queryArgs := []any{granteeType}
	query, queryArgs = addGranteeQuery(query, queryArgs, grantee, granteeType)
	roleRows, err := c.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return observed, err
	}
	defer roleRows.Close() //nolint:errcheck
	if xsql.IsNoRows(err) {
		return observed, nil
	}
	for roleRows.Next() {
		var roleName string
		var roleSchemaName sql.NullString
		rowErr := roleRows.Scan(&roleSchemaName, &roleName)
		if rowErr == nil {
			if roleSchemaName.Valid {
				roleName = fmt.Sprintf("%s.%s", roleSchemaName.String, roleName)
			}
			observed = append(observed, roleName)
		}
	}
	if err := roleRows.Err(); err != nil {
		return observed, err
	}
	return observed, nil
}

type Privilege struct {
	Type       PrivilegeType
	Name       string
	Identifier string
}
type PrivilegeType int

const (
	SystemPrivilegeType PrivilegeType = iota
	SourcePrivilegeType
	SchemaPrivilegeType
	ObjectPrivilegeType // Handles both schema.object and object formats
	UserGroupPrivilegeType
	ColumnKeyPrivilegeType
	StructuredPrivilegeType
)

func (pt PrivilegeType) String() string {
	switch pt {
	case SystemPrivilegeType:
		return "system"
	case SourcePrivilegeType:
		return "source"
	case SchemaPrivilegeType:
		return "schema"
	case ObjectPrivilegeType:
		return "object"
	case UserGroupPrivilegeType:
		return "usergroup"
	case ColumnKeyPrivilegeType:
		return "column"
	case StructuredPrivilegeType:
		return "structured"
	default:
		return "unknown"
	}
}

func (p Privilege) String() string {
	switch p.Type {
	case SystemPrivilegeType:
		return p.Name
	case SourcePrivilegeType:
		return fmt.Sprintf("%s ON REMOTE SOURCE %s", p.Name, p.Identifier)
	case SchemaPrivilegeType:
		return fmt.Sprintf("%s ON SCHEMA %s", p.Name, p.Identifier)
	case ObjectPrivilegeType:
		return fmt.Sprintf("%s ON %s", p.Name, p.Identifier)
	case UserGroupPrivilegeType:
		return fmt.Sprintf("USERGROUP OPERATOR ON USERGROUP %s", p.Identifier)
	case ColumnKeyPrivilegeType:
		return fmt.Sprintf("%s ON CLIENTSIDE ENCRYPTION COLUMN KEY %s", p.Name, p.Identifier)
	case StructuredPrivilegeType:
		return fmt.Sprintf("%s %s", p.Name, p.Identifier)
	default:
		return "unknown"
	}
}

func FormatPrivilegeStrings(privilegeStrings []string, username string) ([]string, error) {
	privileges, err := parsePrivilegeStrings(privilegeStrings, username)
	if err != nil {
		return nil, err
	}
	res := make([]string, 0, len(privileges))
	for _, priv := range privileges {
		res = append(res, priv.String())
	}
	return res, nil
}

func groupPrivilegesByType(privilegeStrings []string, defaultSchema DefaultSchema) ([]string, error) {
	privileges, err := parsePrivilegeStrings(privilegeStrings, defaultSchema)
	if err != nil {
		return nil, err
	}
	groupedPrivileges := groupPrivilegesByTypeAndIdentifier(privileges)
	return groupedPrivileges, nil
}

func parsePrivilegeStrings(privilegeStrings []string, defaultSchema DefaultSchema) ([]Privilege, error) {
	privileges := make([]Privilege, 0, len(privilegeStrings))
	for _, privStr := range privilegeStrings {
		priv, err := parsePrivilegeString(privStr, defaultSchema)
		if err != nil {
			return nil, fmt.Errorf(errParsePrivilege, privStr, err)
		}
		privileges = append(privileges, priv)
	}
	return privileges, nil
}

type privilegePattern struct {
	re    *regexp.Regexp
	build func(m []string, defaultSchema DefaultSchema) Privilege
}

// Parts of single privilege statements specified in https://help.sap.com/docs/SAP_HANA_PLATFORM/4fe29514fd584807ac9f2a04f6754767/20f674e1751910148a8b990d33efbdc5.html?locale=en-US:
// - <system_privilege>
// - <source_privilege> ON REMOTE SOURCE <source_name>
// - <schema_privilege> ON SCHEMA <schema_name>
// - <object_privilege> ON <object_name>
// - <column_key_privilege> ON CLIENTSIDE ENCRYPTION COLUMN KEY <column_encryption_key_name>
// - STRUCTURED PRIVILEGE <structured_privilege>
// - USERGROUP OPERATOR ON USERGROUP <usergroup_name>
var privilegePatterns = []privilegePattern{
	// USERGROUP OPERATOR ON USERGROUP <name>
	{
		re: regexp.MustCompile(`(?i)^\s*(USERGROUP\s+OPERATOR)\s+ON\s+USERGROUP\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: UserGroupPrivilegeType, Name: m[1], Identifier: m[2]}
		},
	},
	// Column key privilege: USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY <name>, currently only USAGE is supported.
	{
		re: regexp.MustCompile(`(?i)^\s*(USAGE)\b\s+ON\s+CLIENTSIDE\s+ENCRYPTION\s+COLUMN\s+KEY\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: ColumnKeyPrivilegeType, Name: "USAGE", Identifier: m[2]}
		},
	},
	// Remote source privilege
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*[A-Za-z])?)\s+ON\s+REMOTE\s+SOURCE\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: SourcePrivilegeType, Name: m[1], Identifier: m[2]}
		},
	},
	// Schema privilege
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*[A-Za-z])?)\s+ON\s+SCHEMA\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: SchemaPrivilegeType, Name: m[1], Identifier: m[2]}
		},
	},
	// Object privilege with schema qualification
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*[A-Za-z])?)\s+ON\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)\.("[^"]+"|[A-Za-z][A-Za-z0-9_]*)\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			identifier := fmt.Sprintf("%s.%s", m[2], m[3])
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: identifier}
		},
	},
	// Object privilege without schema (use default schema)
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*[A-Za-z])?)\s+ON\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)\s*$`),
		build: func(m []string, defaultSchema DefaultSchema) Privilege {
			identifier := fmt.Sprintf("%s.%s", defaultSchema, m[2])
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: identifier}
		},
	},
	// Structured privilege: STRUCTURED PRIVILEGE <name>
	{
		re: regexp.MustCompile(`(?i)^\s*STRUCTURED\s+PRIVILEGE\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: StructuredPrivilegeType, Name: "STRUCTURED PRIVILEGE", Identifier: m[1]}
		},
	},
	// System privilege (standalone)
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*[A-Za-z])?(?:\.[A-Za-z][A-Za-z0-9_]*)?)\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: SystemPrivilegeType, Name: m[1]}
		},
	},
}

func parsePrivilegeString(privStr string, defaultSchema DefaultSchema) (Privilege, error) {
	for _, pp := range privilegePatterns {
		if m := pp.re.FindStringSubmatch(privStr); m != nil {
			return pp.build(m, defaultSchema), nil
		}
	}
	return Privilege{}, fmt.Errorf(errUnknownPrivilege, privStr)
}

func groupPrivilegesByTypeAndIdentifier(privileges []Privilege) []string {
	// Nested map: PrivilegeType -> Identifier -> []PrivilegeName
	privilegeMap := make(map[PrivilegeType]map[string][]string)

	for _, priv := range privileges {
		pt := priv.Type
		id := priv.Identifier
		nm := priv.Name

		if _, ok := privilegeMap[pt]; !ok {
			privilegeMap[pt] = make(map[string][]string)
		}
		privilegeMap[pt][id] = append(privilegeMap[pt][id], nm)
	}

	optimizedPrivileges := make([]string, 0, len(privileges))

	for privType, idMap := range privilegeMap {
		for identifier, names := range idMap {
			// Combine privilege names
			allPrivileges := strings.Join(names, ", ")
			newPrivilege := Privilege{
				Type:       privType,
				Name:       allPrivileges,
				Identifier: identifier,
			}
			optimizedPrivileges = append(optimizedPrivileges, newPrivilege.String())
		}
	}
	return optimizedPrivileges
}

// FilterManagedPrivileges filters the observed privileges based on the management policy
func FilterManagedPrivileges(observed *v1alpha1.UserObservation, specPrivileges []string, prevPrivileges []string, policy, defaultSchema string) (*v1alpha1.UserObservation, error) {
	if observed == nil {
		return nil, errors.New(ErrObservationNil)
	}

	switch policy {
	case "strict":
		return observed, nil
	case "lax":
		defaultPrivilege := fmt.Sprintf("CREATE ANY ON SCHEMA %s", defaultSchema)
		managedPrivs := make([]string, 0, len(observed.Privileges))
		for _, p := range observed.Privileges {
			if p != defaultPrivilege && (slices.Contains(specPrivileges, p) || slices.Contains(prevPrivileges, p)) {
				managedPrivs = append(managedPrivs, p)
			}
		}
		observed.Privileges = managedPrivs
		return observed, nil
	default:
		return observed, fmt.Errorf(ErrUnknownPrivilegeManagementPolicy, policy)
	}
}
