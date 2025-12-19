/*
Copyright 2022 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package user

import (
	"context"
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/user"
)

func TestObserveAuthenticationErrors(t *testing.T) {
	type fields struct {
		client user.UserClient
		log    *MockLogger
	}

	type args struct {
		ctx context.Context
		mg  *v1alpha1.User
	}

	type want struct {
		c   managed.ExternalObservation
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ValidityPeriodError": {
			reason: "Should handle ErrValidityPeriod by setting Unavailable condition and continuing reconciliation",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						username := "DEMO_USER"
						usergroup := "DEFAULT"
						passwordUpToDate := true
						isPasswordLifetimeCheckEnabled := false
						return &v1alpha1.UserObservation{
							Username:                       &username,
							Privileges:                     []string{"CREATE ANY ON SCHEMA DEMO_USER"},
							Roles:                          []string{"PUBLIC"},
							Usergroup:                      &usergroup,
							PasswordUpToDate:               &passwordUpToDate,               // Password is correct, just outside validity period
							IsPasswordLifetimeCheckEnabled: &isPasswordLifetimeCheckEnabled, // Default value
							Parameters:                     make(map[string]string),         // Empty parameters
						}, user.ErrValidityPeriod
					},
				},
				log: &MockLogger{},
			},
			args: args{
				ctx: context.Background(),
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       "DEMO_USER",
							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: false,                   // Match observed
							Parameters:                     make(map[string]string), // Empty parameters
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true, // All fields match despite auth error
				},
				err: nil,
			},
		},
		"UserDeactivatedError": {
			reason: "Should handle ErrUserDeactivated by setting Unavailable condition and continuing reconciliation",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						username := "DEMO_USER"
						usergroup := "DEFAULT"
						passwordUpToDate := true
						isPasswordLifetimeCheckEnabled := false
						return &v1alpha1.UserObservation{
							Username:                       &username,
							Privileges:                     []string{"CREATE ANY ON SCHEMA DEMO_USER"},
							Roles:                          []string{"PUBLIC"},
							Usergroup:                      &usergroup,
							PasswordUpToDate:               &passwordUpToDate,               // Password is correct, user is just deactivated
							IsPasswordLifetimeCheckEnabled: &isPasswordLifetimeCheckEnabled, // Default value
							Parameters:                     make(map[string]string),         // Empty parameters
						}, user.ErrUserDeactivated
					},
				},
				log: &MockLogger{},
			},
			args: args{
				ctx: context.Background(),
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       "DEMO_USER",
							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: false,                   // Match observed
							Parameters:                     make(map[string]string), // Empty parameters
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true, // All fields match despite auth error
				},
				err: nil,
			},
		},
		"UserLockedError": {
			reason: "Should handle ErrUserLocked by setting Unavailable condition and continuing reconciliation",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						username := "DEMO_USER"
						usergroup := "DEFAULT"
						passwordUpToDate := true
						isPasswordLifetimeCheckEnabled := false
						return &v1alpha1.UserObservation{
							Username:                       &username,
							Privileges:                     []string{"CREATE ANY ON SCHEMA DEMO_USER"},
							Roles:                          []string{"PUBLIC"},
							Usergroup:                      &usergroup,
							PasswordUpToDate:               &passwordUpToDate,               // Password is correct, user is just locked
							IsPasswordLifetimeCheckEnabled: &isPasswordLifetimeCheckEnabled, // Default value
							Parameters:                     make(map[string]string),         // Empty parameters
						}, user.ErrUserLocked
					},
				},
				log: &MockLogger{},
			},
			args: args{
				ctx: context.Background(),
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       "DEMO_USER",
							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: false,                   // Match observed
							Parameters:                     make(map[string]string), // Empty parameters
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true, // All fields match despite auth error
				},
				err: nil,
			},
		},
		"AuthErrorWithOutOfDateResource": {
			reason: "Should handle auth error but still return false for ResourceUpToDate when resource is actually out of date",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						username := "DEMO_USER"
						usergroup := "DIFFERENT_GROUP"
						passwordUpToDate := true
						isPasswordLifetimeCheckEnabled := false
						return &v1alpha1.UserObservation{
							Username:                       &username,
							Privileges:                     []string{"CREATE ANY ON SCHEMA DEMO_USER"},
							Roles:                          []string{"PUBLIC"},
							Usergroup:                      &usergroup,                      // Different from desired
							PasswordUpToDate:               &passwordUpToDate,               // Password is correct, just outside validity period
							IsPasswordLifetimeCheckEnabled: &isPasswordLifetimeCheckEnabled, // Default value
							Parameters:                     make(map[string]string),         // Empty parameters
						}, user.ErrValidityPeriod
					},
				},
				log: &MockLogger{},
			},
			args: args{
				ctx: context.Background(),
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       "DEMO_USER",
							Usergroup:                      "DEFAULT",               // Different from observed
							IsPasswordLifetimeCheckEnabled: false,                   // Match observed
							Parameters:                     make(map[string]string), // Empty parameters
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: false, // Resource is actually out of date (usergroup mismatch)
				},
				err: nil,
			},
		},
		"AuthErrorWithUpToDateResource": {
			reason: "Should handle auth error and return true for ResourceUpToDate when resource configuration matches",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						username := "DEMO_USER"
						usergroup := "DEFAULT"
						passwordUpToDate := true
						isPasswordLifetimeCheckEnabled := true
						return &v1alpha1.UserObservation{
							Username:                       &username,
							Privileges:                     []string{"CREATE ANY ON SCHEMA DEMO_USER"},
							Roles:                          []string{"PUBLIC"},
							Usergroup:                      &usergroup,
							PasswordUpToDate:               &passwordUpToDate, // Password is correct, user is just locked
							IsPasswordLifetimeCheckEnabled: &isPasswordLifetimeCheckEnabled,
						}, user.ErrUserLocked
					},
				},
				log: &MockLogger{},
			},
			args: args{
				ctx: context.Background(),
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       "DEMO_USER",
							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: true,
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true, // All configuration matches and password is up to date
				},
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			got, err := e.Observe(tc.args.ctx, tc.args.mg)

			// Check error
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}

			// Check observation result
			if diff := cmp.Diff(tc.want.c, got); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want, +got:\n%s\n", tc.reason, diff)
			}

			// Check that the condition was set to Unavailable for auth errors
			conditions := tc.args.mg.Status.Conditions
			var found bool
			for _, condition := range conditions {
				unavailableCondition := xpv1.Unavailable()
				if condition.Type == unavailableCondition.Type {
					found = true
					if condition.Status != unavailableCondition.Status {
						t.Errorf("\n%s\nExpected condition status %s, got %s", tc.reason, unavailableCondition.Status, condition.Status)
					}
					if condition.Reason != unavailableCondition.Reason {
						t.Errorf("\n%s\nExpected condition reason %s, got %s", tc.reason, unavailableCondition.Reason, condition.Reason)
					}
					break
				}
			}
			if !found {
				t.Errorf("\n%s\nExpected Unavailable condition but none found", tc.reason)
			}

			// Check that appropriate log messages were recorded
			msgs := tc.fields.log.msgs
			found = false
			for _, msg := range msgs {
				if msg == "User validity period error" || msg == "User deactivated error" || msg == "User locked error" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("\n%s\nExpected authentication error log message, but none found in: %v", tc.reason, msgs)
			}
		})
	}
}
