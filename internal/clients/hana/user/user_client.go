package user

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SAP/go-hdb/driver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/privilege"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

// Error types for user authentication issues
var (
	ErrValidityPeriod  = errors.New("connect attempt outside user's validity period")
	ErrUserDeactivated = errors.New("user is deactivated")
	ErrUserLocked      = errors.New("user is locked")
)

const (
	errGrantPrivileges                 = "failed to grant privileges: %w"
	errGrantRoles                      = "failed to grant roles: %w"
	errRevokePrivileges                = "failed to revoke privileges: %w"
	errRevokeRoles                     = "failed to revoke roles: %w"
	errQueryPrivileges                 = "failed to query privileges: %w"
	errQueryRoles                      = "failed to query roles: %w"
	ErrUpdateUserPassword              = "cannot update user password: %w"
	ErrUpdateUserParameters            = "cannot update user parameters: %w"
	ErrUpdateUserUsergroup             = "cannot update user usergroup: %w"
	ErrUpdateUserPasswordLifetimeCheck = "cannot update user password lifetime check: %w"
	ErrGetCorrelationID                = "cannot extract correlation ID from error message: %w"
	ErrCorrIDNotFound                  = "cannot get internal error code for correlation ID %s: %w"
	ErrUnknownInternalErrorCode        = "unknown internal error code %s for correlation ID %s"

	errCodeAuthFailed      = 10
	errCodeValidityPeriod  = 20
	errCodeUserDeactivated = 415
	errCodeUserLocked      = 416

	errIntWrongPassword   = "A10"
	errIntValidityPeriod  = "U03"
	errIntUserDeactivated = "U02"
	errIntUserLocked      = "U06"
)

var validParams = []string{"CLIENT", "LOCALE", "TIME ZONE", "EMAIL ADDRESS", "STATEMENT MEMORY LIMIT", "STATEMENT THREAD LIMIT"}

// UserClient defines the interface for user client operations
type UserClient interface {
	hana.UserQueryClient[v1alpha1.UserParameters, v1alpha1.UserObservation]
	UpdatePrivileges(ctx context.Context, grantee string, toGrant, toRevoke []string) error
	UpdateRoles(ctx context.Context, grantee string, toGrant, toRevoke []string) error
	UpdateParameters(ctx context.Context, username string, parametersToSet, parametersToClear map[string]string) error
	UpdateUsergroup(ctx context.Context, username, usergroup string) error
	UpdatePassword(ctx context.Context, username, password string, forceFirstPasswordChange bool) error
	UpdatePasswordLifetimeCheck(ctx context.Context, username string, isPasswordLifetimeCheckEnabled bool) error
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

// Observe checks the state of the user
func (c Client) Read(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (*v1alpha1.UserObservation, error) {

	var username, usergroup string
	var createdAt, lastPasswordChangeTime time.Time
	var restrictedUser, isPasswordLifetimeCheckEnabled bool

	query := "SELECT USER_NAME, " +
		"USERGROUP_NAME, " +
		"CREATE_TIME, " +
		"LAST_PASSWORD_CHANGE_TIME, " +
		"IS_RESTRICTED, " +
		"IS_PASSWORD_LIFETIME_CHECK_ENABLED " +
		"FROM SYS.USERS " +
		"WHERE USER_NAME = ?"

	err := c.QueryRowContext(ctx, query, parameters.Username).Scan(
		&username,
		&usergroup,
		&createdAt,
		&lastPasswordChangeTime,
		&restrictedUser,
		&isPasswordLifetimeCheckEnabled,
	)

	if xsql.IsNoRows(err) {
		return &v1alpha1.UserObservation{}, nil
	} else if err != nil {
		return &v1alpha1.UserObservation{}, err
	}

	observed := &v1alpha1.UserObservation{
		Username:                       &username,
		Usergroup:                      &usergroup,
		CreatedAt:                      metav1.NewTime(createdAt),
		LastPasswordChangeTime:         metav1.NewTime(lastPasswordChangeTime),
		RestrictedUser:                 &restrictedUser,
		IsPasswordLifetimeCheckEnabled: &isPasswordLifetimeCheckEnabled,
	}

	observed.Parameters, err = queryParameters(ctx, c, parameters.Username)
	if err != nil {
		return observed, err
	}

	observed.Privileges, err = c.QueryPrivileges(ctx, parameters.Username, privilege.GranteeTypeUser)
	if err != nil {
		return observed, fmt.Errorf(errQueryPrivileges, err)
	}

	observed.Roles, err = c.QueryRoles(ctx, parameters.Username, privilege.GranteeTypeUser)
	if err != nil {
		return observed, fmt.Errorf(errQueryRoles, err)
	}

	passwordUpToDate, err := validateCredentials(ctx, c, parameters.Username, password)
	observed.PasswordUpToDate = &passwordUpToDate

	return observed, err
}

func queryParameters(ctx context.Context, c Client, username string) (map[string]string, error) {
	observed := make(map[string]string)
	query := "SELECT USER_NAME, " +
		"PARAMETER, " +
		"VALUE " +
		"FROM SYS.USER_PARAMETERS " +
		"WHERE USER_NAME = ?"
	rows, err := c.QueryContext(ctx, query, username)
	if xsql.IsNoRows(err) {
		return observed, nil
	} else if err != nil {
		return observed, err
	}
	defer rows.Close() //nolint:errcheck

	for rows.Next() {
		var username, key, value string
		rowErr := rows.Scan(&username, &key, &value)
		if rowErr == nil {
			observed[key] = value
		}
	}
	if err := rows.Err(); err != nil {
		return observed, err
	}
	return observed, nil
}

func validateCredentials(ctx context.Context, c Client, username string, password string) (bool, error) {
	query := fmt.Sprintf(`VALIDATE USER %s PASSWORD "%s"`, username, password)
	_, err := c.ExecContext(ctx, query)
	var dbError driver.Error
	if errors.As(err, &dbError) {
		switch dbError.Code() {
		case errCodeValidityPeriod:
			return true, ErrValidityPeriod
		case errCodeUserDeactivated:
			return true, ErrUserDeactivated
		case errCodeUserLocked:
			return true, ErrUserLocked
		case errCodeAuthFailed:
			correlationID := extractCorrelationID(err.Error())
			if correlationID == "" {
				return true, fmt.Errorf(ErrGetCorrelationID, err)
			}
			query := `SELECT INTERNAL_ERROR_CODE FROM SYS.AUTHENTICATION_ERROR_DETAILS WHERE CORRELATION_ID = ?`
			var internalErrorCode string
			scanErr := c.QueryRowContext(ctx, query, correlationID).Scan(&internalErrorCode)
			if scanErr != nil {
				return true, fmt.Errorf(ErrCorrIDNotFound, correlationID, scanErr)
			}
			switch internalErrorCode {
			case errIntWrongPassword:
				return false, nil
			case errIntValidityPeriod:
				return true, ErrValidityPeriod
			case errIntUserDeactivated:
				return true, ErrUserDeactivated
			case errIntUserLocked:
				return true, ErrUserLocked
			default:
				return true, fmt.Errorf(ErrUnknownInternalErrorCode, internalErrorCode, correlationID)
			}
		}
	}
	return true, err
}

// Create a new user
func (c Client) Create(ctx context.Context, parameters *v1alpha1.UserParameters, args ...any) error {
	query := "CREATE USER %s"
	if parameters.RestrictedUser {
		query = "CREATE RESTRICTED USER %s"
	}
	query = fmt.Sprintf(query, parameters.Username)

	if parameters.Authentication.Password.PasswordSecretRef != nil {
		password := args[0].(string)
		if password == "" {
			return errors.New("cannot get user password")
		}
		query += fmt.Sprintf(` PASSWORD "%s"`, password)
		if !parameters.Authentication.Password.ForceFirstPasswordChange {
			query += " NO FORCE_FIRST_PASSWORD_CHANGE"
		}
	}

	if len(parameters.Parameters) > 0 {
		query = setParameters(query, parameters.Parameters)
	}

	if parameters.Usergroup != "" {
		query += fmt.Sprintf(" SET USERGROUP %s", parameters.Usergroup)
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return err
	}

	if len(parameters.Privileges) > 0 {
		if err := c.GrantPrivileges(ctx, c.username, parameters.Username, parameters.Privileges); err != nil {
			return fmt.Errorf(errGrantPrivileges, err)
		}
	}

	if len(parameters.Roles) > 0 {
		if err := c.GrantRoles(ctx, c.username, parameters.Username, parameters.Roles); err != nil {
			return fmt.Errorf(errGrantRoles, err)
		}
	}

	if !parameters.IsPasswordLifetimeCheckEnabled {
		err := c.UpdatePasswordLifetimeCheck(ctx, parameters.Username, parameters.IsPasswordLifetimeCheckEnabled)
		if err != nil {
			return err
		}
	}

	return nil
}

func setParameters(query string, parameters map[string]string) string {
	newParams := make([]string, 0, len(parameters))
	for key, value := range parameters {
		key = strings.ToUpper(key)
		if slices.Contains(validParams, key) {
			newParams = append(newParams, fmt.Sprintf("%s = '%s'", key, value))
		}
	}
	if len(newParams) == 0 {
		return query
	}
	return query + " SET PARAMETER " + strings.Join(newParams, ", ")
}

// UpdatePassword returns an error about not being able to update the password
func (c Client) UpdatePassword(ctx context.Context, username string, password string, forceFirstPasswordChange bool) error {
	query := fmt.Sprintf(`ALTER USER %s PASSWORD "%s"`, username, password)
	if !forceFirstPasswordChange {
		query += " NO FORCE_FIRST_PASSWORD_CHANGE"
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf(ErrUpdateUserPassword, err)
	}
	return nil
}

func (c Client) UpdatePrivileges(ctx context.Context, grantee string, toGrant, toRevoke []string) error {
	if len(toGrant) > 0 {
		if err := c.GrantPrivileges(ctx, c.username, grantee, toGrant); err != nil {
			return err
		}
	}

	if len(toRevoke) > 0 {
		if err := c.RevokePrivileges(ctx, c.username, grantee, toRevoke); err != nil {
			return err
		}
	}

	return nil
}

func (c Client) UpdateRoles(ctx context.Context, grantee string, toGrant, toRevoke []string) error {
	if len(toGrant) > 0 {
		if err := c.GrantRoles(ctx, c.username, grantee, toGrant); err != nil {
			return err
		}
	}

	if len(toRevoke) > 0 {
		if err := c.RevokeRoles(ctx, c.username, grantee, toRevoke); err != nil {
			return err
		}
	}

	return nil
}

// UpdateParameters updates the parameters of the user
func (c Client) UpdateParameters(ctx context.Context, username string, parametersToSet map[string]string, parametersToClear map[string]string) error {
	query := fmt.Sprintf("ALTER USER %s", username)

	if len(parametersToSet) > 0 {
		query += " SET PARAMETER"
		for key, value := range parametersToSet {
			key = strings.ToUpper(key)
			if slices.Contains(validParams, key) {
				query += fmt.Sprintf(" %s = '%s',", key, value)
			}
		}
		query = strings.TrimSuffix(query, ",")
	}

	if len(parametersToClear) > 0 {
		query += " CLEAR PARAMETER"
		for key := range parametersToClear {
			key = strings.ToUpper(key)
			if slices.Contains(validParams, key) {
				query += fmt.Sprintf(" %s,", key)
			}
		}
		query = strings.TrimSuffix(query, ",")
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf(ErrUpdateUserParameters, err)
	}
	return nil
}

// UpdateUsergroup updates the usergroup of the user
func (c Client) UpdateUsergroup(ctx context.Context, username string, usergroup string) error {
	query := fmt.Sprintf("ALTER USER %s", username)

	if usergroup != "" {
		query += fmt.Sprintf(" SET USERGROUP %s", usergroup)
	} else {
		query += " UNSET USERGROUP"
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf(ErrUpdateUserUsergroup, err)
	}
	return nil
}

func (c Client) UpdatePasswordLifetimeCheck(ctx context.Context, username string, isPasswordLifetimeCheckEnabled bool) error {
	query := fmt.Sprintf("ALTER USER %s", username)

	if isPasswordLifetimeCheckEnabled {
		query += " ENABLE"
	} else {
		query += " DISABLE"
	}

	query += " PASSWORD LIFETIME"

	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf(ErrUpdateUserPasswordLifetimeCheck, err)
	}
	return nil
}

// Delete deletes the user
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.UserParameters) error {

	query := fmt.Sprintf("DROP USER %s", parameters.Username)

	if _, err := c.ExecContext(ctx, query); err != nil {
		return err
	}

	return nil
}

// GetDefaultSchema returns the default schema for the user
func (c Client) GetDefaultSchema() string {
	// The default schema for a user is always the same as the username
	return c.username
}

// extractCorrelationID extracts the correlation ID from HANA error messages
// Returns empty string if no correlation ID is found
func extractCorrelationID(errText string) string {
	// Look for the pattern "correlation ID 'XXXXXXXX'"
	startMarker := "correlation ID '"
	startIndex := strings.Index(errText, startMarker)
	if startIndex == -1 {
		return ""
	}

	// Move to the actual start of the ID
	startIndex += len(startMarker)

	// Find the closing quote
	endIndex := strings.Index(errText[startIndex:], "'")
	if endIndex == -1 {
		return ""
	}

	return errText[startIndex : startIndex+endIndex]
}
