package auditpolicy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/utils"
)

type AuditPolicyClient interface {
	hana.QueryClient[v1alpha1.AuditPolicyParameters, v1alpha1.AuditPolicyObservation]
	RecreatePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
	UpdateRetentionDays(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
	UpdateEnablePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
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

// Read checks the state of the audit policy
func (c Client) Read(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) (*v1alpha1.AuditPolicyObservation, error) {

	observed := &v1alpha1.AuditPolicyObservation{}

	query := getSelectSql()
	policyActionRows, err := c.QueryContext(ctx, query, parameters.PolicyName)
	if err != nil {
		return nil, err
	}
	defer policyActionRows.Close() //nolint:errcheck

	for policyActionRows.Next() {
		var policyName string
		var eventStatus string
		var eventAction sql.NullString
		var principalName sql.NullString
		var exceptPrincipalName sql.NullString
		var principalType sql.NullString
		var eventLevel string
		var retentionPeriod sql.NullInt64
		var isActive string
		err = policyActionRows.Scan(&policyName, &eventStatus, &eventAction, &principalName, &exceptPrincipalName, &principalType, &eventLevel, &retentionPeriod, &isActive)
		if err != nil {
			return nil, err
		}
		observed.PolicyName = policyName
		observed.AuditStatus = strings.TrimSuffix(eventStatus, " EVENTS")
		if eventAction.Valid {
			actionString := readActionString(eventAction, principalName, exceptPrincipalName, principalType)
			observed.AuditActions = append(observed.AuditActions, actionString)
		}
		observed.AuditLevel = eventLevel
		if retentionPeriod.Valid {
			rp := int(retentionPeriod.Int64)
			observed.AuditTrailRetention = &rp
		}
		if isActive == "TRUE" {
			observed.Enabled = new(true)
		} else {
			observed.Enabled = new(false)
		}
	}

	if err = policyActionRows.Err(); err != nil {
		return nil, err
	}

	return observed, nil
}

// Create a new audit policy
func (c Client) Create(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {

	for _, query := range prepareCreateSql(parameters) {
		_, err := c.ExecContext(ctx, query)
		if err != nil {
			return err
		}
		if parameters.Enabled != nil && *parameters.Enabled {
			enableQuery := prepareEnableDisablePolicySql(parameters)
			_, err = c.ExecContext(ctx, enableQuery)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c Client) RecreatePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
	// Drop and recreate the policy to change actions/status/level
	err := c.Delete(ctx, parameters)
	if err != nil {
		return err
	}
	err = c.Create(ctx, parameters)
	if err != nil {
		return err
	}

	return nil
}

func (c Client) UpdateRetentionDays(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {

	query := prepareUpdateRetentionDaysSql(parameters)
	_, err := c.ExecContext(ctx, query)
	if err != nil {
		return err
	}

	return nil
}

func (c Client) UpdateEnablePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {

	query := prepareEnableDisablePolicySql(parameters)
	_, err := c.ExecContext(ctx, query)
	if err != nil {
		return err
	}

	return nil
}

// Delete an existing audit policy
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {

	query := prepareDeleteSql(parameters)

	_, err := c.ExecContext(ctx, query)

	if err != nil {
		return err
	}

	return nil
}

func OptimizeAuditActions(actionStrings []string) ([]string, error) {
	for i := range actionStrings {
		actionStrings[i] = strings.ReplaceAll(actionStrings[i], " FOR PRINCIPALS ALL USERS", "")
	}
	groupedActions := []string{}
	parsedActions := []parsedAction{}
	for _, actionString := range actionStrings {
		actions, err := parseIntoActions(actionString)
		if err != nil {
			return nil, fmt.Errorf("error parsing action string '%s': %w", actionString, err)
		}
		parsedActions = append(parsedActions, actions...)
	}
	grouped := groupActions(parsedActions)
	if len(grouped) > 1 {
		return nil, errors.New("only one audit action is supported at the moment")
	}
	for _, ga := range grouped {
		groupedActions = append(groupedActions, stringifyParsedAction(ga))
	}
	return groupedActions, nil
}

type parsedPrincipal struct {
	user      string
	usergroup string
}

type parsedAction struct {
	actionNames    []string
	auditFor       []parsedPrincipal
	auditExceptFor []parsedPrincipal
}

func readActionString(eventAction sql.NullString, principalName sql.NullString, exceptPrincipalName sql.NullString, principalType sql.NullString) string {
	actionString := eventAction.String
	var nameOfPrincipal string
	if principalName.Valid {
		actionString += " FOR PRINCIPALS"
		nameOfPrincipal = principalName.String
	} else if exceptPrincipalName.Valid {
		actionString += " EXCEPT FOR PRINCIPALS"
		nameOfPrincipal = exceptPrincipalName.String
	}
	if principalType.Valid {
		actionString += " " + principalType.String
	}
	if nameOfPrincipal != "" {
		actionString += " " + nameOfPrincipal
	}
	return actionString
}

func parseUserEntry(userEntry string) (parsedPrincipal, error) {
	userEntry = strings.TrimSpace(userEntry)
	if strings.HasPrefix(userEntry, "USERGROUP ") {
		usergroup := strings.TrimPrefix(userEntry, "USERGROUP ")
		return parsedPrincipal{usergroup: usergroup}, nil
	} else if strings.HasPrefix(userEntry, "USER ") {
		user := strings.TrimPrefix(userEntry, "USER ")
		return parsedPrincipal{user: user}, nil
	}
	return parsedPrincipal{}, fmt.Errorf("invalid principal entry: %s", userEntry)
}

func parseUsersList(usersPart string) ([]parsedPrincipal, error) {
	usersPart = strings.TrimSpace(usersPart)
	parsedUsers := []parsedPrincipal{}

	if strings.HasPrefix(usersPart, "PRINCIPALS ") {
		usersList := strings.TrimPrefix(usersPart, "PRINCIPALS ")
		for _, userEntry := range strings.Split(usersList, ",") {
			parsedUser, err := parseUserEntry(userEntry)
			if err != nil {
				return nil, err
			}
			parsedUsers = append(parsedUsers, parsedUser)
		}
	} else {
		for _, userEntry := range strings.Split(usersPart, ",") {
			parsedUsers = append(parsedUsers, parsedPrincipal{user: userEntry})
		}
	}
	return parsedUsers, nil
}

func parseIntoActions(action string) ([]parsedAction, error) {
	actions := []parsedAction{}
	actionNames := action

	partialAction := parsedAction{}
	if exceptForParts := strings.SplitN(action, " EXCEPT FOR ", 2); len(exceptForParts) == 2 {
		actionNames = exceptForParts[0]
		afterExceptFor := exceptForParts[1]
		partialUsers, err := parseUsersList(afterExceptFor)
		if err != nil {
			return nil, err
		}
		partialAction.auditExceptFor = partialUsers
	} else if forParts := strings.SplitN(exceptForParts[0], " FOR ", 2); len(forParts) == 2 {
		actionNames = forParts[0]
		afterFor := forParts[1]
		partialUsers, err := parseUsersList(afterFor)
		if err != nil {
			return nil, err
		}
		partialAction.auditFor = partialUsers
	}
	for _, actionName := range strings.Split(actionNames, ",") {
		action := parsedAction{
			actionNames:    []string{strings.ToUpper(strings.TrimSpace(actionName))},
			auditFor:       partialAction.auditFor,
			auditExceptFor: partialAction.auditExceptFor,
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func getUniqueString(input []string) string {
	sorted := make([]string, len(input))
	copy(sorted, input)
	sort.Strings(sorted)
	sorted = utils.ArrayToUpper(sorted)
	uniqueString := strings.Join(sorted, ",")
	return uniqueString
}

func updatePrincipals(actionNames []string, principals []parsedPrincipal, principalMap map[string][]string, principalSelfMap map[string][]parsedPrincipal) {
	users := []string{}
	usergroups := []string{}
	for _, principal := range principals {
		if principal.user != "" {
			users = append(users, principal.user)
		} else if principal.usergroup != "" {
			usergroups = append(usergroups, principal.usergroup)
		}
	}
	uniqueUsersString := getUniqueString(users)
	uniqueUsergroupsString := getUniqueString(usergroups)
	uniqueString := uniqueUsersString + ";" + uniqueUsergroupsString
	principalMap[uniqueString] = append(principalMap[uniqueString], actionNames...)
	principalSelfMap[uniqueString] = principals
}

func splitActions(actions []parsedAction) (noUserMap []string, forPrincipalsMap map[string][]string, forPrincipalsSelfMap map[string][]parsedPrincipal, exceptForPrincipalsMap map[string][]string, exceptForPrincipalsSelfMap map[string][]parsedPrincipal) {
	noUserMap = []string{}
	forPrincipalsMap = make(map[string][]string)
	forPrincipalsSelfMap = make(map[string][]parsedPrincipal)
	exceptForPrincipalsMap = make(map[string][]string)
	exceptForPrincipalsSelfMap = make(map[string][]parsedPrincipal)

	for _, action := range actions {
		switch {
		case len(action.auditFor) > 0:
			updatePrincipals(action.actionNames, action.auditFor, forPrincipalsMap, forPrincipalsSelfMap)
		case len(action.auditExceptFor) > 0:
			updatePrincipals(action.actionNames, action.auditExceptFor, exceptForPrincipalsMap, exceptForPrincipalsSelfMap)
		default:
			noUserMap = append(noUserMap, action.actionNames...)
		}
	}
	return noUserMap, forPrincipalsMap, forPrincipalsSelfMap, exceptForPrincipalsMap, exceptForPrincipalsSelfMap
}

func groupActions(actions []parsedAction) []parsedAction {
	noUserMap, forPrincipalsMap, forPrincipalsSelfMap, exceptForPrincipalsMap, exceptForPrincipalsSelfMap := splitActions(actions)

	groupedActions := make([]parsedAction, 0, 1+len(forPrincipalsMap)+len(exceptForPrincipalsMap))
	groupedActions = append(groupedActions, parsedAction{actionNames: noUserMap})

	for principalsString, actionNames := range forPrincipalsMap {
		principals := forPrincipalsSelfMap[principalsString]
		groupedActions = append(groupedActions, parsedAction{
			actionNames: actionNames,
			auditFor:    principals,
		})
	}
	for principalsString, actionNames := range exceptForPrincipalsMap {
		principals := exceptForPrincipalsSelfMap[principalsString]
		groupedActions = append(groupedActions, parsedAction{
			actionNames:    actionNames,
			auditExceptFor: principals,
		})
	}
	return groupedActions
}

func stringifyUserList(principals []parsedPrincipal) string {
	if len(principals) == 0 {
		return ""
	}

	principalStrs := []string{}
	for _, principal := range principals {
		if principal.user != "" {
			principalStrs = append(principalStrs, fmt.Sprintf("USER %s", principal.user))
		} else if principal.usergroup != "" {
			principalStrs = append(principalStrs, fmt.Sprintf("USERGROUP %s", principal.usergroup))
		}
	}
	return "PRINCIPALS " + strings.Join(principalStrs, ", ")
}

func stringifyParsedAction(pa parsedAction) string {
	actionStr := strings.Join(pa.actionNames, ", ")
	if pa.auditFor != nil {
		if principals := stringifyUserList(pa.auditFor); principals != "" {
			actionStr += fmt.Sprintf(" FOR %s", principals)
		}
	} else if pa.auditExceptFor != nil {
		if principals := stringifyUserList(pa.auditExceptFor); principals != "" {
			actionStr += fmt.Sprintf(" EXCEPT FOR %s", principals)
		}
	}
	return actionStr
}

func prepareCreateSql(parameters *v1alpha1.AuditPolicyParameters) []string {
	queryLeft := fmt.Sprintf(`CREATE AUDIT POLICY "%s" AUDITING %s`, utils.EscapeDoubleQuotes(parameters.PolicyName), parameters.AuditStatus)
	queryRight := fmt.Sprintf("LEVEL %s TRAIL TYPE TABLE RETENTION %d", parameters.AuditLevel, *parameters.AuditTrailRetention)

	queries := []string{}
	if len(parameters.AuditActions) == 0 {
		return []string{fmt.Sprintf("%s %s", queryLeft, queryRight)}
	}

	for _, action := range parameters.AuditActions {
		queries = append(queries, fmt.Sprintf("%s %s %s", queryLeft, action, queryRight))
	}
	return queries
}

func getSelectSql() string {
	return "SELECT AUDIT_POLICY_NAME, EVENT_STATUS, EVENT_ACTION, PRINCIPAL_NAME, EXCEPT_PRINCIPAL_NAME, PRINCIPAL_TYPE, EVENT_LEVEL, RETENTION_PERIOD, IS_AUDIT_POLICY_ACTIVE FROM AUDIT_POLICIES WHERE AUDIT_POLICY_NAME = ?"
}

func prepareEnableDisablePolicySql(parameters *v1alpha1.AuditPolicyParameters) string {
	return fmt.Sprintf(`ALTER AUDIT POLICY "%s" %s`, utils.EscapeDoubleQuotes(parameters.PolicyName), map[bool]string{true: "ENABLE", false: "DISABLE"}[*parameters.Enabled])
}

func prepareUpdateRetentionDaysSql(parameters *v1alpha1.AuditPolicyParameters) string {
	return fmt.Sprintf(`ALTER AUDIT POLICY "%s" SET RETENTION %d`, utils.EscapeDoubleQuotes(parameters.PolicyName), *parameters.AuditTrailRetention)
}

func prepareDeleteSql(parameters *v1alpha1.AuditPolicyParameters) string {
	return fmt.Sprintf(`DROP AUDIT POLICY "%s"`, utils.EscapeDoubleQuotes(parameters.PolicyName))
}
