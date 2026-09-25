/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package auditpolicy

import (
	"context"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/auditpolicy"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

// MockLogger is a mock implementation of logging.Logger
type MockLogger struct{}

// Debug logs debug messages.
func (l *MockLogger) Debug(_ string, _ ...any) {}

// Info logs info messages.
func (l *MockLogger) Info(_ string, _ ...any) {}

// WithValues returns a logger with the specified key-value pairs.
func (l *MockLogger) WithValues(_ ...any) logging.Logger { return l }

type mockAuditPolicyClient struct {
	MockRead                func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) (observed *v1alpha1.AuditPolicyObservation, err error)
	MockCreate              func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
	MockDelete              func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
	MockRecreatePolicy      func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
	MockUpdateRetentionDays func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
	MockUpdateEnablePolicy  func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
}

func (m mockAuditPolicyClient) Read(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) (observed *v1alpha1.AuditPolicyObservation, err error) {
	return m.MockRead(ctx, parameters)
}

func (m mockAuditPolicyClient) Create(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
	return m.MockCreate(ctx, parameters)
}

func (m mockAuditPolicyClient) RecreatePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
	return m.MockRecreatePolicy(ctx, parameters)
}

func (m mockAuditPolicyClient) UpdateRetentionDays(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
	return m.MockUpdateRetentionDays(ctx, parameters)
}

func (m mockAuditPolicyClient) UpdateEnablePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
	return m.MockUpdateEnablePolicy(ctx, parameters)
}

func (m mockAuditPolicyClient) Delete(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
	return m.MockDelete(ctx, parameters)
}

func TestConnect(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		kube      client.Client
		usage     resource.LegacyTracker
		newClient func(db xsql.DB) auditpolicy.Client
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   error
	}{
		"ErrNotSchema": {
			reason: "An error should be returned if the managed resource is not a *AuditPolicy",
			args: args{
				mg: nil,
			},
			want: errors.New(errNotAuditPolicy),
		},
		"ErrTrackProviderConfigUsage": {
			reason: "An error should be returned if we can't track our ProviderConfig usage",
			fields: fields{
				usage: resource.LegacyTrackerFn(func(ctx context.Context, mg resource.LegacyManaged) error { return errBoom }), //nolint:staticcheck // Legacy cluster-scoped resources are intentionally preserved.
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{},
			},
			want: errors.Wrap(errBoom, errTrackPCUsage),
		},
		"ErrGetProviderConfig": {
			reason: "An error should be returned if we can't get our ProviderConfig",
			fields: fields{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(errBoom),
				},
				usage: resource.LegacyTrackerFn(func(ctx context.Context, mg resource.LegacyManaged) error { return nil }), //nolint:staticcheck // Legacy cluster-scoped resources are intentionally preserved.
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ClusterManagedResourceSpec: xpv2.ClusterManagedResourceSpec{
							ProviderConfigReference: &xpv2.Reference{},
						},
					},
				},
			},
			want: errors.Wrap(errBoom, errGetPC),
		},
		"ErrMissingConnectionSecret": {
			reason: "An error should be returned if our ProviderConfig doesn't specify a connection secret",
			fields: fields{
				kube: &test.MockClient{
					// We call get to populate the Database struct, then again
					// to populate the (empty) ProviderConfig struct, resulting
					// in a ProviderConfig with a nil connection secret.
					MockGet: test.NewMockGetFn(nil),
				},
				usage: resource.LegacyTrackerFn(func(ctx context.Context, mg resource.LegacyManaged) error { return nil }), //nolint:staticcheck // Legacy cluster-scoped resources are intentionally preserved.
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ClusterManagedResourceSpec: xpv2.ClusterManagedResourceSpec{
							ProviderConfigReference: &xpv2.Reference{},
						},
					},
				},
			},
			want: errors.New(errNoSecretRef),
		},
		"ErrGetConnectionSecret": {
			reason: "An error should be returned if we can't get our ProviderConfig's connection secret",
			fields: fields{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						switch o := obj.(type) {
						case *apisv1alpha1.ProviderConfig:
							o.Spec.Credentials.ConnectionSecretRef = &xpv2.SecretReference{}
						case *corev1.Secret:
							return errBoom
						}
						return nil
					}),
				},
				usage: resource.LegacyTrackerFn(func(ctx context.Context, mg resource.LegacyManaged) error { return nil }), //nolint:staticcheck // Legacy cluster-scoped resources are intentionally preserved.
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ClusterManagedResourceSpec: xpv2.ClusterManagedResourceSpec{
							ProviderConfigReference: &xpv2.Reference{},
						},
					},
				},
			},
			want: errors.Wrap(errBoom, errGetSecret),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &connector{kube: tc.fields.kube, usage: tc.fields.usage, newClient: tc.fields.newClient}
			_, err := e.Connect(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Connect(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client auditpolicy.AuditPolicyClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotAuditPolicy": {
			reason: "An error should be returned if the managed resource is not an *AuditPolicy",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotAuditPolicy),
			},
		},
		"ErrCreate": {
			reason: "An error should be returned if the client Create method returns an error",
			fields: fields{
				client: mockAuditPolicyClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ForProvider: v1alpha1.AuditPolicyParameters{},
					},
				},
			},
			want: want{
				err: errors.Wrap(errBoom, errCreatePolicy),
			},
		},
		"Successful": {
			reason: "No error should be returned if the client Create method is successful",
			fields: fields{
				client: mockAuditPolicyClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ForProvider: v1alpha1.AuditPolicyParameters{},
					},
				},
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			_, err := e.Create(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Create(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client auditpolicy.AuditPolicyClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotAuditPolicy": {
			reason: "An error should be returned if the managed resource is not an *AuditPolicy",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotAuditPolicy),
			},
		},
		"ErrDelete": {
			reason: "An error should be returned if the client Delete method returns an error",
			fields: fields{
				client: mockAuditPolicyClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ForProvider: v1alpha1.AuditPolicyParameters{},
					},
				},
			},
			want: want{
				err: errors.Wrap(errBoom, errDropPolicy),
			},
		},
		"Successful": {
			reason: "No error should be returned if the client Delete method is successful",
			fields: fields{
				client: mockAuditPolicyClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ForProvider: v1alpha1.AuditPolicyParameters{},
					},
				},
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			_, err := e.Delete(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Delete(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestRead(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client auditpolicy.AuditPolicyClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		err error
		ob  managed.ExternalObservation
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotAuditPolicy": {
			reason: "An error should be returned if the managed resource is not an *AuditPolicy",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotAuditPolicy),
			},
		},
		"ErrRead": {
			reason: "An error should be returned if the client Read method returns an error",
			fields: fields{
				client: mockAuditPolicyClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) (observed *v1alpha1.AuditPolicyObservation, err error) {
						return nil, errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ForProvider: v1alpha1.AuditPolicyParameters{},
					},
				},
			},
			want: want{
				err: errors.Wrap(errBoom, errSelectPolicy),
			},
		},
		"Successful": {
			reason: "No error should be returned if the client Read method is successful",
			fields: fields{
				client: mockAuditPolicyClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) (observed *v1alpha1.AuditPolicyObservation, err error) {
						return &v1alpha1.AuditPolicyObservation{}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ForProvider: v1alpha1.AuditPolicyParameters{},
					},
				},
			},
			want: want{
				err: nil,
				ob: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			_, err := e.Observe(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestRecreatePolicy(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client auditpolicy.AuditPolicyClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotAuditPolicy": {
			reason: "An error should be returned if the managed resource is not an *AuditPolicy",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotAuditPolicy),
			},
		},
		"ErrRecreatePolicy": {
			reason: "An error should be returned if the client RecreatePolicy method returns an error",
			fields: fields{
				client: mockAuditPolicyClient{
					MockRecreatePolicy: func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ForProvider: v1alpha1.AuditPolicyParameters{
							AuditLevel: "INFO",
						},
					},
				},
			},
			want: want{
				err: errors.Wrap(errBoom, errUpdatePolicy),
			},
		},
		"Successful": {
			reason: "No error should be returned if the client RecreatePolicy method is successful",
			fields: fields{
				client: mockAuditPolicyClient{
					MockRecreatePolicy: func(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.AuditPolicy{
					Spec: v1alpha1.AuditPolicySpec{
						ForProvider: v1alpha1.AuditPolicyParameters{
							AuditLevel: "INFO",
						},
					},
				},
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			_, err := e.Update(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.RecreatePolicy(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestBuildDesiredParameters(t *testing.T) {
	cases := map[string]struct {
		reason string
		cr     *v1alpha1.AuditPolicy
		want   *v1alpha1.AuditPolicyParameters
	}{
		"UserGroupPrincipals": {
			reason: "The desired parameters should carry and upper-case a single user group principal",
			cr: &v1alpha1.AuditPolicy{
				Spec: v1alpha1.AuditPolicySpec{
					ForProvider: v1alpha1.AuditPolicyParameters{
						PolicyName:   "signavio_technical_user_connect",
						AuditStatus:  "successful",
						AuditActions: []string{"connect"},
						AuditPrincipals: []v1alpha1.AuditPrincipal{
							{Type: "usergroup", Name: "technical_user_group"},
						},
						AuditLevel: "info",
					},
				},
			},
			want: &v1alpha1.AuditPolicyParameters{
				PolicyName:   "SIGNAVIO_TECHNICAL_USER_CONNECT",
				AuditStatus:  "SUCCESSFUL",
				AuditActions: []string{"CONNECT"},
				AuditPrincipals: []v1alpha1.AuditPrincipal{
					{Type: "USERGROUP", Name: "TECHNICAL_USER_GROUP"},
				},
				AuditLevel: "INFO",
			},
		},
		"NoPrincipals": {
			reason: "The desired parameters should carry a nil principal list when none are configured",
			cr: &v1alpha1.AuditPolicy{
				Spec: v1alpha1.AuditPolicySpec{
					ForProvider: v1alpha1.AuditPolicyParameters{
						PolicyName:   "demo_audit_policy",
						AuditStatus:  "successful",
						AuditActions: []string{"connect"},
						AuditLevel:   "info",
					},
				},
			},
			want: &v1alpha1.AuditPolicyParameters{
				PolicyName:      "DEMO_AUDIT_POLICY",
				AuditStatus:     "SUCCESSFUL",
				AuditActions:    []string{"CONNECT"},
				AuditPrincipals: nil,
				AuditLevel:      "INFO",
			},
		},
		"ExceptMixedPrincipals": {
			reason: "The desired parameters should carry and upper-case an ordered, mixed principal list with the EXCEPT flag",
			cr: &v1alpha1.AuditPolicy{
				Spec: v1alpha1.AuditPolicySpec{
					ForProvider: v1alpha1.AuditPolicyParameters{
						PolicyName:   "except_principals_audit_policy1",
						AuditStatus:  "successful",
						AuditActions: []string{"actions"},
						AuditPrincipals: []v1alpha1.AuditPrincipal{
							{Type: "user", Name: "user1"},
							{Type: "usergroup", Name: "usergroup1"},
							{Type: "user", Name: "user2"},
							{Type: "usergroup", Name: "usergroup2"},
						},
						ExceptPrincipals: true,
						AuditLevel:       "critical",
					},
				},
			},
			want: &v1alpha1.AuditPolicyParameters{
				PolicyName:   "EXCEPT_PRINCIPALS_AUDIT_POLICY1",
				AuditStatus:  "SUCCESSFUL",
				AuditActions: []string{"ACTIONS"},
				AuditPrincipals: []v1alpha1.AuditPrincipal{
					{Type: "USER", Name: "USER1"},
					{Type: "USERGROUP", Name: "USERGROUP1"},
					{Type: "USER", Name: "USER2"},
					{Type: "USERGROUP", Name: "USERGROUP2"},
				},
				ExceptPrincipals: true,
				AuditLevel:       "CRITICAL",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := buildDesiredParameters(tc.cr)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("\n%s\nbuildDesiredParameters(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestPrincipalsDiffer(t *testing.T) {
	cases := map[string]struct {
		reason   string
		observed *v1alpha1.AuditPolicyObservation
		desired  *v1alpha1.AuditPolicyParameters
		want     bool
	}{
		"NoPrincipalsEqual": {
			reason:   "No principals on either side is not a difference",
			observed: &v1alpha1.AuditPolicyObservation{},
			desired:  &v1alpha1.AuditPolicyParameters{},
			want:     false,
		},
		"SamePrincipalsDifferentOrder": {
			reason: "Principal comparison is order-independent",
			observed: &v1alpha1.AuditPolicyObservation{
				AuditPrincipals: []v1alpha1.AuditPrincipal{
					{Type: "USERGROUP", Name: "TECHNICAL_USER_GROUP"},
					{Type: "USER", Name: "MONITORING_ADMIN"},
				},
			},
			desired: &v1alpha1.AuditPolicyParameters{
				AuditPrincipals: []v1alpha1.AuditPrincipal{
					{Type: "USER", Name: "MONITORING_ADMIN"},
					{Type: "USERGROUP", Name: "TECHNICAL_USER_GROUP"},
				},
			},
			want: false,
		},
		"AddedPrincipal": {
			reason: "Adding a principal is a difference",
			observed: &v1alpha1.AuditPolicyObservation{
				AuditPrincipals: []v1alpha1.AuditPrincipal{
					{Type: "USER", Name: "MONITORING_ADMIN"},
				},
			},
			desired: &v1alpha1.AuditPolicyParameters{
				AuditPrincipals: []v1alpha1.AuditPrincipal{
					{Type: "USER", Name: "MONITORING_ADMIN"},
					{Type: "USERGROUP", Name: "TECHNICAL_USER_GROUP"},
				},
			},
			want: true,
		},
		"FlippedExceptPrincipals": {
			reason: "Flipping ExceptPrincipals with principals configured is a difference",
			observed: &v1alpha1.AuditPolicyObservation{
				AuditPrincipals: []v1alpha1.AuditPrincipal{
					{Type: "USER", Name: "MONITORING_ADMIN"},
				},
				ExceptPrincipals: false,
			},
			desired: &v1alpha1.AuditPolicyParameters{
				AuditPrincipals: []v1alpha1.AuditPrincipal{
					{Type: "USER", Name: "MONITORING_ADMIN"},
				},
				ExceptPrincipals: true,
			},
			want: true,
		},
		"ExceptPrincipalsIgnoredWhenNoPrincipals": {
			reason: "ExceptPrincipals is irrelevant when no principals are configured",
			observed: &v1alpha1.AuditPolicyObservation{
				ExceptPrincipals: false,
			},
			desired: &v1alpha1.AuditPolicyParameters{
				ExceptPrincipals: true,
			},
			want: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := principalsDiffer(tc.observed, tc.desired)
			if got != tc.want {
				t.Errorf("\n%s\nprincipalsDiffer(...): want %v, got %v", tc.reason, tc.want, got)
			}
		})
	}
}
