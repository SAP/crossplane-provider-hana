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
	Type          PrivilegeType
	Name          string
	Identifier    string
	SubIdentifier string
	IsGrantable   bool
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
// Updated to handle special identifiers with embedded quotes like "SCHE""M'A"
var roleRegex = regexp.MustCompile(`(?i)^\s*(` + identifierPattern + `(?:\.` + identifierPattern + `)?)` + adminOptionRegex + `\s*$`)

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

// formatSpecialObjectPrivilege handles special object privilege formatting patterns
func formatSpecialObjectPrivilege(name, identifier string) string {
	switch {
	case strings.HasPrefix(identifier, "PSE "):
		pseName := strings.TrimPrefix(identifier, "PSE ")
		return fmt.Sprintf(`%s ON PSE %s`, name, pseName)
	case strings.HasPrefix(identifier, "JWT PROVIDER "):
		providerName := strings.TrimPrefix(identifier, "JWT PROVIDER ")
		return fmt.Sprintf(`%s ON JWT PROVIDER %s`, name, providerName)
	case strings.HasPrefix(identifier, "SAML PROVIDER "):
		providerName := strings.TrimPrefix(identifier, "SAML PROVIDER ")
		return fmt.Sprintf(`%s ON SAML PROVIDER %s`, name, providerName)
	case strings.HasPrefix(identifier, "X509 PROVIDER "):
		providerName := strings.TrimPrefix(identifier, "X509 PROVIDER ")
		return fmt.Sprintf(`%s ON X509 PROVIDER %s`, name, providerName)
	default:
		// Regular object privilege
		return fmt.Sprintf(`%s ON "%s"`, name, utils.EscapeDoubleQuotes(identifier))
	}
}

// formatObjectPrivilege handles object privilege formatting
func (p Privilege) formatObjectPrivilege() string {
	if p.SubIdentifier != "" {
		// Both parsing and database cases: schema and object are separate fields
		return fmt.Sprintf(`%s ON "%s"."%s"`, p.Name, utils.EscapeDoubleQuotes(p.Identifier), utils.EscapeDoubleQuotes(p.SubIdentifier))
	}
	return formatSpecialObjectPrivilege(p.Name, p.Identifier)
}

func (p Privilege) baseString() string {
	switch p.Type {
	case SystemPrivilegeType:
		return p.Name
	case SourcePrivilegeType:
		return fmt.Sprintf(`%s ON REMOTE SOURCE "%s"`, p.Name, utils.EscapeDoubleQuotes(p.Identifier))
	case SchemaPrivilegeType:
		return fmt.Sprintf(`%s ON SCHEMA "%s"`, p.Name, utils.EscapeDoubleQuotes(p.Identifier))
	case ObjectPrivilegeType:
		return p.formatObjectPrivilege()
	case UserGroupPrivilegeType:
		return fmt.Sprintf(`USERGROUP OPERATOR ON USERGROUP "%s"`, utils.EscapeDoubleQuotes(p.Identifier))
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

// FormatPrivilegeStringsWithPreprocessing safely preprocesses and formats privilege strings.
// Use this function when processing privilege strings that may contain outer quotes or different escaping.
func FormatPrivilegeStringsWithPreprocessing(privilegeStrings []string, username string) ([]string, error) {
	// Pre-process privilege strings to remove outer quotes securely and normalize escaping
	processedStrings := utils.PreprocessPrivilegeStrings(privilegeStrings)

	privileges, err := parsePrivilegeStrings(processedStrings, username)
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

// identifierPattern matches both simple identifiers and special identifiers with embedded quotes
// Special identifiers: "..." where " can be escaped as ""
// Simple identifiers: Much more permissive to handle system identifiers and edge cases
const identifierPattern = `(?:"(?:[^"]|"")*"|[^\s]+)`

// cleanIdentifier removes outer quotes from an identifier and unescapes inner quotes
func cleanIdentifier(identifier string) string {
	if len(identifier) >= 2 && identifier[0] == '"' && identifier[len(identifier)-1] == '"' {
		// Remove outer quotes and unescape inner doubled quotes
		return strings.ReplaceAll(identifier[1:len(identifier)-1], `""`, `"`)
	}
	return identifier
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
		re: regexp.MustCompile(`(?i)^\s*(USERGROUP\s+OPERATOR)\s+ON\s+USERGROUP\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: UserGroupPrivilegeType, Name: m[1], Identifier: cleanIdentifier(m[2]), IsGrantable: m[3] != ""}
		},
	},
	// Column key privilege: USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY <name>, currently only USAGE is supported.
	{
		re: regexp.MustCompile(`(?i)^\s*(USAGE)\b\s+ON\s+CLIENTSIDE\s+ENCRYPTION\s+COLUMN\s+KEY\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: ColumnKeyPrivilegeType, Name: "USAGE", Identifier: cleanIdentifier(m[2]), IsGrantable: m[3] != ""}
		},
	},
	// PSE privilege: <privilege> ON PSE <name> (treated as object privilege)
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*?[A-Za-z])?)\s+ON\s+PSE\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: "PSE " + cleanIdentifier(m[2]), IsGrantable: m[3] != ""}
		},
	},
	// JWT PROVIDER privilege: <privilege> ON JWT PROVIDER <name> (treated as object privilege)
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*?[A-Za-z])?)\s+ON\s+JWT\s+PROVIDER\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: "JWT PROVIDER " + cleanIdentifier(m[2]), IsGrantable: m[3] != ""}
		},
	},
	// SAML PROVIDER privilege: <privilege> ON SAML PROVIDER <name> (treated as object privilege)
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*?[A-Za-z])?)\s+ON\s+SAML\s+PROVIDER\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: "SAML PROVIDER " + cleanIdentifier(m[2]), IsGrantable: m[3] != ""}
		},
	},
	// X509 PROVIDER privilege: <privilege> ON X509 PROVIDER <name> (treated as object privilege)
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*?[A-Za-z])?)\s+ON\s+X509\s+PROVIDER\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: "X509 PROVIDER " + cleanIdentifier(m[2]), IsGrantable: m[3] != ""}
		},
	},
	// Remote source privilege
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*?[A-Za-z])?)\s+ON\s+REMOTE\s+SOURCE\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: SourcePrivilegeType, Name: m[1], Identifier: cleanIdentifier(m[2]), IsGrantable: m[3] != ""}
		},
	},
	// Schema privilege
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*?[A-Za-z])?)\s+ON\s+SCHEMA\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: SchemaPrivilegeType, Name: m[1], Identifier: cleanIdentifier(m[2]), IsGrantable: m[3] != ""}
		},
	},
	// Object privilege with schema qualification
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*?[A-Za-z])?)\s+ON\s+(` + identifierPattern + `)\.(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: cleanIdentifier(m[2]), SubIdentifier: cleanIdentifier(m[3]), IsGrantable: m[4] != ""}
		},
	},
	// Object privilege without schema (use default schema)
	{
		re: regexp.MustCompile(`(?i)^\s*([A-Za-z](?:[A-Za-z\s]*?[A-Za-z])?)\s+ON\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, defaultSchema DefaultSchema) Privilege {
			return Privilege{Type: ObjectPrivilegeType, Name: m[1], Identifier: defaultSchema, SubIdentifier: cleanIdentifier(m[2]), IsGrantable: m[3] != ""}
		},
	},
	// Structured privilege: STRUCTURED PRIVILEGE <name>
	{
		re: regexp.MustCompile(`(?i)^\s*STRUCTURED\s+PRIVILEGE\s+(` + identifierPattern + `)` + grantOptionRegex + `\s*$`),
		build: func(m []string, _ DefaultSchema) Privilege {
			return Privilege{Type: StructuredPrivilegeType, Name: "STRUCTURED PRIVILEGE", Identifier: cleanIdentifier(m[1]), IsGrantable: m[2] != ""}
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
		// For object privileges, use the full object reference as the identifier
		identifier := p.Identifier
		if p.Type == ObjectPrivilegeType && p.SubIdentifier != "" {
			identifier = p.Identifier + "." + p.SubIdentifier
		}

		key := groupKey{p.Type, identifier, p.IsGrantable}
		groupsMap[key] = append(groupsMap[key], p.Name)
	}

	res := make([]PrivilegeGroup, 0, len(groupsMap))
	for key, names := range groupsMap {
		// Generate the base string (e.g. "SELECT, INSERT ON SCHEMA X")
		var temp Privilege
		if key.pType == ObjectPrivilegeType && strings.Contains(key.identifier, ".") {
			// Split back to Identifier and SubIdentifier for object privileges
			parts := strings.SplitN(key.identifier, ".", 2)
			temp = Privilege{Type: key.pType, Name: strings.Join(names, ", "), Identifier: parts[0], SubIdentifier: parts[1]}
		} else {
			temp = Privilege{Type: key.pType, Name: strings.Join(names, ", "), Identifier: key.identifier}
		}
		res = append(res, PrivilegeGroup{
			Body:        temp.baseString(),
			IsGrantable: key.isGrantable,
			Type:        key.pType,
		})
	}
	return res
}

func GetDefaultPrivilege(defaultSchema string) string {
	return fmt.Sprintf(`CREATE ANY ON SCHEMA "%s" WITH GRANT OPTION`, defaultSchema)
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

// createSystemPrivilege creates a system privilege
func createSystemPrivilege(privilege string, isGrantable bool) Privilege {
	return Privilege{
		Type:        SystemPrivilegeType,
		Name:        privilege,
		IsGrantable: isGrantable,
	}
}

// createSchemaPrivilege creates a schema privilege
func createSchemaPrivilege(privilege string, schemaName sql.NullString, isGrantable bool) Privilege {
	return Privilege{
		Type:        SchemaPrivilegeType,
		Name:        privilege,
		Identifier:  schemaName.String,
		IsGrantable: isGrantable,
	}
}

// createSpecialObjectPrivilege creates special object privileges (PSE, JWT/SAML/X509 PROVIDER)
func createSpecialObjectPrivilege(privilege, objectType string, objectName sql.NullString, isGrantable bool) Privilege {
	return Privilege{
		Type:        ObjectPrivilegeType,
		Name:        privilege,
		Identifier:  objectType + " " + objectName.String,
		IsGrantable: isGrantable,
	}
}

// createRegularObjectPrivilege creates regular object privileges
func createRegularObjectPrivilege(privilege string, schemaName, objectName sql.NullString, isGrantable bool) Privilege {
	return Privilege{
		Type:          ObjectPrivilegeType,
		Name:          privilege,
		Identifier:    schemaName.String,
		SubIdentifier: objectName.String,
		IsGrantable:   isGrantable,
	}
}

func handlePrivilegeRows(privRows *sql.Rows) (Privilege, error) {
	var objectType, privilege string
	var isGrantable bool
	var schemaName, objectName sql.NullString
	if err := privRows.Scan(&objectType, &privilege, &schemaName, &objectName, &isGrantable); err != nil {
		return Privilege{}, err
	}

	switch objectType {
	case "SYSTEMPRIVILEGE":
		return createSystemPrivilege(privilege, isGrantable), nil
	case "SCHEMA":
		return createSchemaPrivilege(privilege, schemaName, isGrantable), nil
	case "SOURCE":
		return Privilege{
			Type:        SourcePrivilegeType,
			Name:        privilege,
			Identifier:  objectName.String,
			IsGrantable: isGrantable,
		}, nil
	case "USERGROUP":
		return Privilege{
			Type:        UserGroupPrivilegeType,
			Name:        privilege,
			Identifier:  objectName.String,
			IsGrantable: isGrantable,
		}, nil
	case "CLIENTSIDE ENCRYPTION COLUMN KEY":
		return Privilege{
			Type:        ColumnKeyPrivilegeType,
			Name:        privilege,
			Identifier:  objectName.String,
			IsGrantable: isGrantable,
		}, nil
	case "STRUCTURED_PRIVILEGE":
		return Privilege{
			Type:        StructuredPrivilegeType,
			Name:        "STRUCTURED PRIVILEGE",
			Identifier:  objectName.String,
			IsGrantable: isGrantable,
		}, nil
	case "PSE", "JWT PROVIDER", "SAML PROVIDER", "X509 PROVIDER":
		return createSpecialObjectPrivilege(privilege, objectType, objectName, isGrantable), nil
	default:
		return createRegularObjectPrivilege(privilege, schemaName, objectName, isGrantable), nil
	}
}

// privilegeBaseKey uniquely identifies a privilege ignoring its grantable state.
type privilegeBaseKey struct {
	pType         PrivilegeType
	name          string
	identifier    string
	subIdentifier string
}

func privilegeToBaseKey(p Privilege) privilegeBaseKey {
	return privilegeBaseKey{p.Type, p.Name, p.Identifier, p.SubIdentifier}
}

// PrivilegesEqualIgnoringGrantable returns true when desired and observed privilege
// string slices refer to the same privileges, ignoring admin/grant option entirely.
// Used when privilegeGrantablePolicy is "lax".
func PrivilegesEqualIgnoringGrantable(desired, observed []string, defaultSchema DefaultSchema) (bool, error) {
	toGrant, toRevoke, err := PrivilegesDiffIgnoringGrantable(desired, observed, defaultSchema)
	if err != nil {
		return false, err
	}
	return len(toGrant) == 0 && len(toRevoke) == 0, nil
}

// PrivilegesDiffIgnoringGrantable returns toGrant and toRevoke slices, ignoring
// admin/grant option when comparing desired vs observed. Used when
// privilegeGrantablePolicy is "lax".
func PrivilegesDiffIgnoringGrantable(desired, observed []string, defaultSchema DefaultSchema) (toGrant, toRevoke []string, err error) {
	desiredPrivs, err := parsePrivilegeStrings(desired, defaultSchema)
	if err != nil {
		return nil, nil, err
	}
	observedPrivs, err := parsePrivilegeStrings(observed, defaultSchema)
	if err != nil {
		return nil, nil, err
	}

	observedKeys := make(map[privilegeBaseKey]bool, len(observedPrivs))
	for _, p := range observedPrivs {
		observedKeys[privilegeToBaseKey(p)] = true
	}

	desiredKeys := make(map[privilegeBaseKey]bool, len(desiredPrivs))
	for _, d := range desiredPrivs {
		desiredKeys[privilegeToBaseKey(d)] = true
		if !observedKeys[privilegeToBaseKey(d)] {
			toGrant = append(toGrant, d.String())
		}
	}

	for _, o := range observedPrivs {
		if !desiredKeys[privilegeToBaseKey(o)] {
			toRevoke = append(toRevoke, o.String())
		}
	}

	return toGrant, toRevoke, nil
}
