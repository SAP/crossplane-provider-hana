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
	"github.com/SAP/crossplane-provider-hana/internal/utils"
)

const (
	errUnknownPrivilege                 = "unknown type of privilege: %s"
	errParsePrivilege                   = "failed to parse privilege %s: %w"
	ErrUnknownPrivilegeManagementPolicy = "unknown privilege management policy: %s"
	ErrObservationNil                   = "observed user observation cannot be nil"
	errUnknownRole                      = "failed to parse role: %s"
	errRoleInvalidGrantOption           = "failed to parse role with grantable option: %s"
	errPrivilegeInvalidGrantOption      = "failed to parse privilege with grant option: %s"
	errPrivilegeInvalidAdminOption      = "failed to parse privilege with admin option: %s"
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

func (c *PrivilegeClient) GrantPrivileges(ctx context.Context, grantor DefaultSchema, grantee Grantee, privilegeStrings []string) error {
	if len(privilegeStrings) == 0 {
		return nil
	}

	groupedObjects, err := groupPrivilegesByType(privilegeStrings, grantor)
	if err != nil {
		return err
	}

	for _, g := range groupedObjects {
		query := fmt.Sprintf("GRANT %s TO %s", g.Body, grantee)
		if g.IsGrantable {
			if g.Type == SystemPrivilegeType {
				query += " WITH ADMIN OPTION"
			} else {
				query += " WITH GRANT OPTION"
			}
		}
		if _, err := c.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func (c *PrivilegeClient) GrantRoles(ctx context.Context, _ DefaultSchema, grantee Grantee, roleNames []string) error {
	if len(roleNames) == 0 {
		return nil
	}

	// Group roles by isGrantable because they must be in separate SQL statements if options differ
	adminRoles := []string{}
	normalRoles := []string{}

	for _, rStr := range roleNames {
		role, err := parseRoleString(rStr)
		if err != nil {
			return err
		}
		if role.IsGrantable {
			adminRoles = append(adminRoles, role.Name)
		} else {
			normalRoles = append(normalRoles, role.Name)
		}
	}

	if len(normalRoles) > 0 {
		query := fmt.Sprintf("GRANT %s TO %s", strings.Join(normalRoles, ", "), grantee)
		if _, err := c.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	if len(adminRoles) > 0 {
		query := fmt.Sprintf("GRANT %s TO %s WITH ADMIN OPTION", strings.Join(adminRoles, ", "), grantee)
		if _, err := c.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func (c *PrivilegeClient) RevokePrivileges(ctx context.Context, defaultSchema DefaultSchema, grantee Grantee, privilegeStrings []string) error {
	groupedObjects, err := groupPrivilegesByType(privilegeStrings, defaultSchema)
	if err != nil {
		return err
	}

	for _, g := range groupedObjects {
		// Revoke statement does not use WITH OPTION suffix
		query := fmt.Sprintf("REVOKE %s FROM %s", g.Body, grantee)
		if _, err := c.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func (c *PrivilegeClient) RevokeRoles(ctx context.Context, _ DefaultSchema, grantee Grantee, roleNames []string) error {
	if len(roleNames) == 0 {
		return nil
	}

	namesToRevoke := make([]string, 0, len(roleNames))
	for _, rStr := range roleNames {
		role, err := parseRoleString(rStr)
		if err != nil {
			return err
		}
		namesToRevoke = append(namesToRevoke, role.Name)
	}

	query := fmt.Sprintf("REVOKE %s FROM %s", strings.Join(namesToRevoke, ", "), grantee)
	_, err := c.ExecContext(ctx, query)
	return err
}

func addGranteeQuery(query string, grantee string, granteeType GranteeType) (string, []any) {
	queryArgs := []any{granteeType}
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
	query := "SELECT OBJECT_TYPE, PRIVILEGE, SCHEMA_NAME, OBJECT_NAME, IS_GRANTABLE FROM GRANTED_PRIVILEGES WHERE GRANTEE_TYPE = ?"
	query, queryArgs := addGranteeQuery(query, grantee, granteeType)

	privRows, err := c.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return observed, err
	}
	defer privRows.Close() //nolint:errcheck
	for privRows.Next() {
		privilege, err := handlePrivilegeRows(privRows)
		if err != nil {
			return observed, err
		}
		observed = append(observed, privilege.String())
	}
	if err := privRows.Err(); err != nil {
		return observed, err
	}
	return observed, nil
}

func (c *PrivilegeClient) QueryRoles(ctx context.Context, grantee Grantee, granteeType GranteeType) ([]string, error) {
	observed := []string{}
	query := "SELECT ROLE_SCHEMA_NAME, ROLE_NAME, IS_GRANTABLE FROM GRANTED_ROLES WHERE GRANTEE_TYPE = ?"
	query, queryArgs := addGranteeQuery(query, grantee, granteeType)
	roleRows, err := c.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return observed, err
	}
	defer roleRows.Close() //nolint:errcheck
	for roleRows.Next() {
		var roleName string
		var isGrantable bool
		var roleSchemaName sql.NullString
		if err := roleRows.Scan(&roleSchemaName, &roleName, &isGrantable); err != nil {
			return observed, err
		}
		fullName := roleName
		if roleSchemaName.Valid {
			fullName = fmt.Sprintf("%s.%s", roleSchemaName.String, roleName)
		}
		r := Role{Name: fullName, IsGrantable: isGrantable}
		observed = append(observed, r.String())
	}
	if err := roleRows.Err(); err != nil {
		return observed, err
	}
	return observed, nil
}

type Privilege struct {
	Type        PrivilegeType
	Name        string
	Identifier  string
	IsGrantable bool
}

type Role struct {
	Name        string
	IsGrantable bool
}

func (r Role) String() string {
	if r.IsGrantable {
		return r.Name + " WITH ADMIN OPTION"
	}
	return r.Name
}

// PrivilegeGroup holds aggregated names to build optimized SQL: GRANT Name1, Name2 ON ...
type PrivilegeGroup struct {
	Body        string
	IsGrantable bool
	Type        PrivilegeType
}

// Common regex suffix for grantable options
const (
	adminOptionRegex = `(?i)(\s+WITH\s+ADMIN\s+OPTION)?`
	grantOptionRegex = `(?i)(\s+WITH\s+GRANT\s+OPTION)?`
)

// Role regex supports: "ROLE_NAME", ROLE_NAME, SCHEMA.ROLE_NAME, and optional WITH ADMIN OPTION
var roleRegex = regexp.MustCompile(`(?i)^\s*("[^"]+"|[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)?)` + adminOptionRegex + `\s*$`)

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

func (p Privilege) baseString() string {
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

// String() returns the full privilege string including grantable option if applicable
func (p Privilege) String() string {
	base := p.baseString()
	if !p.IsGrantable {
		return base
	}
	if p.Type == SystemPrivilegeType {
		return base + " WITH ADMIN OPTION"
	}
	return base + " WITH GRANT OPTION"
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

func groupPrivilegesByType(privilegeStrings []string, defaultSchema DefaultSchema) ([]PrivilegeGroup, error) {
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

func parseRoleString(roleStr string) (Role, error) {
	m := roleRegex.FindStringSubmatch(roleStr)
	if m != nil {
		return Role{
			Name:        m[1],
			IsGrantable: m[2] != "",
		}, nil
	}
	// Check for invalid grant option usage
	upperStr := strings.ToUpper(strings.TrimSpace(roleStr))
	if strings.HasSuffix(upperStr, "WITH GRANT OPTION") {
		return Role{}, fmt.Errorf(errRoleInvalidGrantOption, roleStr)
	}
	return Role{}, fmt.Errorf(errUnknownRole, roleStr)
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
		re: regexp.MustCompile(`(?i)^\s*(USERGROUP\s+OPERATOR)\s+ON\s+USERGROUP\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: UserGroupPrivilegeType, Name: m[1], Identifier: m[2], IsGrantable: m[3] != ""}
		},
	},
	// Column key privilege: USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY <name>, currently only USAGE is supported.
	{
		re: regexp.MustCompile(`(?i)^\s*(USAGE)\b\s+ON\s+CLIENTSIDE\s+ENCRYPTION\s+COLUMN\s+KEY\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: ColumnKeyPrivilegeType, Name: "USAGE", Identifier: m[2], IsGrantable: m[3] != ""}
		},
	},
	// Remote source privilege
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*[A-Za-z])?)\s+ON\s+REMOTE\s+SOURCE\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: SourcePrivilegeType, Name: m[1], Identifier: m[2], IsGrantable: m[3] != ""}
		},
	},
	// Schema privilege
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*[A-Za-z])?)\s+ON\s+SCHEMA\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: SchemaPrivilegeType, Name: m[1], Identifier: m[2], IsGrantable: m[3] != ""}
		},
	},
	// Object privilege with schema qualification
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*[A-Za-z])?)\s+ON\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)\.("[^"]+"|[A-Za-z][A-Za-z0-9_]*)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: fmt.Sprintf("%s.%s", m[2], m[3]), IsGrantable: m[4] != ""}
		},
	},
	// Object privilege without schema (use default schema)
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*[A-Za-z])?)\s+ON\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)` + grantOptionRegex + `\s*$`),
		build: func(m []string, defaultSchema DefaultSchema) Privilege {
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: fmt.Sprintf("%s.%s", defaultSchema, m[2]), IsGrantable: m[3] != ""}
		},
	},
	// Structured privilege: STRUCTURED PRIVILEGE <name>
	{
		re: regexp.MustCompile(`(?i)^\s*STRUCTURED\s+PRIVILEGE\s+("[^"]+"|[A-Za-z][A-Za-z0-9_]*)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: StructuredPrivilegeType, Name: "STRUCTURED PRIVILEGE", Identifier: m[1], IsGrantable: m[2] != ""}
		},
	},
	// System privilege (standalone)
	{
		// Changed [A-Za-z\s]* to [A-Za-z\s]*? (added a question mark to enable non-greedy matching)
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*?[A-Za-z])?(?:\.[A-Za-z][A-Za-z0-9_]*)?)` + adminOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: SystemPrivilegeType, Name: m[1], IsGrantable: m[2] != ""}
		},
	},
}

func parsePrivilegeString(privStr string, defaultSchema DefaultSchema) (Privilege, error) {
	upper := strings.ToUpper(strings.TrimSpace(privStr))
	// - System privilege: no " ON " clause, and suffix is WITH ADMIN OPTION.
	// - Non-system privilege: has " ON " and suffix is WITH GRANT OPTION.
	hasOn := strings.Contains(upper, " ON ")
	for _, pp := range privilegePatterns {
		if m := pp.re.FindStringSubmatch(privStr); m != nil {
			priv := pp.build(m, defaultSchema)
			// semantic validation
			if priv.Type == SystemPrivilegeType {
				// system privilege must NOT have ON
				if hasOn {
					break
				}
				// system privilege must NOT use GRANT OPTION
				if strings.HasSuffix(upper, "WITH GRANT OPTION") {
					return Privilege{}, fmt.Errorf(errPrivilegeInvalidGrantOption, privStr)
				}
			}
			return priv, nil
		}
	}
	// fallback suffix validation
	if !hasOn && strings.HasSuffix(upper, "WITH GRANT OPTION") {
		return Privilege{}, fmt.Errorf(errPrivilegeInvalidAdminOption, privStr)
	}
	if hasOn && strings.HasSuffix(upper, "WITH ADMIN OPTION") {
		return Privilege{}, fmt.Errorf(errPrivilegeInvalidAdminOption, privStr)
	}

	return Privilege{}, fmt.Errorf(errUnknownPrivilege, privStr)
}

// groupPrivilegesByTypeAndIdentifier groups by Type, Identifier, and NOW IsGrantable status
func groupPrivilegesByTypeAndIdentifier(privileges []Privilege) []PrivilegeGroup {
	type groupKey struct {
		pType       PrivilegeType
		identifier  string
		isGrantable bool
	}

	groupsMap := make(map[groupKey][]string)
	for _, p := range privileges {
		key := groupKey{p.Type, p.Identifier, p.IsGrantable}
		groupsMap[key] = append(groupsMap[key], p.Name)
	}

	res := make([]PrivilegeGroup, 0, len(groupsMap))
	for key, names := range groupsMap {
		// Generate the base string (e.g. "SELECT, INSERT ON SCHEMA X")
		temp := Privilege{Type: key.pType, Name: strings.Join(names, ", "), Identifier: key.identifier}
		res = append(res, PrivilegeGroup{
			Body:        temp.baseString(),
			IsGrantable: key.isGrantable,
			Type:        key.pType,
		})
	}
	return res
}

func GetDefaultPrivilege(defaultSchema string) string {
	return fmt.Sprintf(`CREATE ANY ON SCHEMA "%s" WITH GRANT OPTION`, utils.EscapeDoubleQuotes(defaultSchema))
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
		defaultPrivilege := GetDefaultPrivilege(defaultSchema)
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

func handlePrivilegeRows(privRows *sql.Rows) (Privilege, error) {
	var objectType, privilege string
	var isGrantable bool
	var schemaName, objectName sql.NullString
	if err := privRows.Scan(&objectType, &privilege, &schemaName, &objectName, &isGrantable); err != nil {
		return Privilege{}, err
	}

	p := Privilege{IsGrantable: isGrantable}
	switch objectType {
	case "SYSTEMPRIVILEGE":
		p.Type = SystemPrivilegeType
		p.Name = privilege
	case "SCHEMA":
		p.Type = SchemaPrivilegeType
		p.Name = privilege
		p.Identifier = schemaName.String
	case "SOURCE":
		p.Type = SourcePrivilegeType
		p.Name = privilege
		p.Identifier = objectName.String
	case "USERGROUP":
		p.Type = UserGroupPrivilegeType
		p.Name = privilege
		p.Identifier = objectName.String
	case "CLIENTSIDE ENCRYPTION COLUMN KEY":
		p.Type = ColumnKeyPrivilegeType
		p.Name = privilege
		p.Identifier = objectName.String
	case "STRUCTURED_PRIVILEGE":
		p.Type = StructuredPrivilegeType
		p.Name = "STRUCTURED PRIVILEGE"
		p.Identifier = objectName.String
	default:
		p.Type = ObjectPrivilegeType
		p.Name = privilege
		p.Identifier = fmt.Sprintf("%s.%s", schemaName.String, objectName.String)
	}
	return p, nil
}
