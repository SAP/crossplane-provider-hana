/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package rolegroup

import (
	"context"
	"testing"

	"errors"
	"fmt"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/rolegroup"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
)

// MockLogger is a mock implementation of logging.Logger
type MockLogger struct{}

// Debug logs debug messages.
func (l *MockLogger) Debug(_ string, _ ...any) {}

// Info logs info messages.
func (l *MockLogger) Info(_ string, _ ...any) {}

// WithValues returns a logger with the specified key-value pairs.
func (l *MockLogger) WithValues(_ ...any) logging.Logger { return l }

type mockClient struct {
	MockRead                   func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) (observed *v1alpha1.RolegroupObservation, err error)
	MockCreate                 func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error
	MockDelete                 func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error
	MockUpdateDisableRoleAdmin func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error
}

func (m mockClient) Read(ctx context.Context, parameters *v1alpha1.RolegroupParameters) (observed *v1alpha1.RolegroupObservation, err error) {
	return m.MockRead(ctx, parameters)
}

func (m mockClient) Create(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {
	return m.MockCreate(ctx, parameters)
}

func (m mockClient) Delete(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {
	return m.MockDelete(ctx, parameters)
}

func (m mockClient) UpdateDisableRoleAdmin(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {
	return m.MockUpdateDisableRoleAdmin(ctx, parameters)
}

func TestConnect(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		kube      client.Client
		usage     resource.Tracker
		newClient func(db xsql.DB) rolegroup.Client
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
		"ErrNotRoleGroup": {
			reason: "An error should be returned if the managed resource is not a *Rolegroup",
			args: args{
				mg: nil,
			},
			want: errors.New(errNotRolegroup),
		},
		"ErrTrackProviderConfigUsage": {
			reason: "An error should be returned if we can't track our ProviderConfig usage",
			fields: fields{
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return errBoom }),
			},
			args: args{
				mg: &v1alpha1.Rolegroup{},
			},
			want: fmt.Errorf(errTrackPCUsage, errBoom),
		},
		"ErrGetProviderConfig": {
			reason: "An error should be returned if we can't get our ProviderConfig",
			fields: fields{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(errBoom),
				},
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.Rolegroup{
					Spec: v1alpha1.RolegroupSpec{
						ResourceSpec: xpv1.ResourceSpec{
							ProviderConfigReference: &xpv1.Reference{},
						},
					},
				},
			},
			want: fmt.Errorf(errGetPC, errBoom),
		},
		"ErrMissingConnectionSecret": {
			reason: "An error should be returned if our ProviderConfig doesn't specify a connection secret",
			fields: fields{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil),
				},
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.Rolegroup{
					Spec: v1alpha1.RolegroupSpec{
						ResourceSpec: xpv1.ResourceSpec{
							ProviderConfigReference: &xpv1.Reference{},
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
							o.Spec.Credentials.ConnectionSecretRef = &xpv1.SecretReference{}
						case *corev1.Secret:
							return errBoom
						}
						return nil
					}),
				},
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.Rolegroup{
					Spec: v1alpha1.RolegroupSpec{
						ResourceSpec: xpv1.ResourceSpec{
							ProviderConfigReference: &xpv1.Reference{},
						},
					},
				},
			},
			want: fmt.Errorf(errGetSecret, errBoom),
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

func TestObserve(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client rolegroup.RolegroupClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
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
		"ErrNotRoleGroup": {
			reason: "An error should be returned if the managed resource is not a *Rolegroup",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotRolegroup),
			},
		},
		"ErrObserve": {
			reason: "Any errors encountered while observing the Rolegroup should be returned",
			fields: fields{
				client: mockClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) (observed *v1alpha1.RolegroupObservation, err error) {
						return nil, errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Rolegroup{
					Spec: v1alpha1.RolegroupSpec{
						ForProvider: v1alpha1.RolegroupParameters{
							RolegroupName: "DEMO_ROLEGROUP",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errSelectRolegroup, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully observe a Rolegroup",
			fields: fields{
				client: mockClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) (observed *v1alpha1.RolegroupObservation, err error) {
						return &v1alpha1.RolegroupObservation{
							RolegroupName: "",
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Rolegroup{
					Spec: v1alpha1.RolegroupSpec{
						ForProvider: v1alpha1.RolegroupParameters{
							RolegroupName: "DEMO_ROLEGROUP",
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
			got, err := e.Observe(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.c, got); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client rolegroup.RolegroupClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		c   managed.ExternalCreation
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotRoleGroup": {
			reason: "An error should be returned if the managed resource is not a *Rolegroup",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotRolegroup),
			},
		},
		"ErrCreate": {
			reason: "Any errors encountered while creating the Rolegroup should be returned",
			fields: fields{
				client: mockClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Rolegroup{
					Spec: v1alpha1.RolegroupSpec{
						ForProvider: v1alpha1.RolegroupParameters{
							RolegroupName: "DEMO_ROLEGROUP",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errCreateRolegroup, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully create a Rolegroup",
			fields: fields{
				client: mockClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Rolegroup{
					Spec: v1alpha1.RolegroupSpec{
						ForProvider: v1alpha1.RolegroupParameters{
							RolegroupName: "DEMO_ROLEGROUP",
						},
					},
				},
			},
			want: want{
				err: nil,
				c:   managed.ExternalCreation{ConnectionDetails: managed.ConnectionDetails{}},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			got, err := e.Create(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Create(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.c, got); diff != "" {
				t.Errorf("\n%s\ne.Create(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client rolegroup.RolegroupClient
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
		"ErrNotRoleGroup": {
			reason: "An error should be returned if the managed resource is not a *Rolegroup",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotRolegroup),
			},
		},
		"ErrDelete": {
			reason: "Any errors encountered while deleting the Rolegroup should be returned",
			fields: fields{
				client: mockClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Rolegroup{
					Spec: v1alpha1.RolegroupSpec{
						ForProvider: v1alpha1.RolegroupParameters{
							RolegroupName: "DEMO_ROLEGROUP",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errDropRolegroup, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete a Rolegroup",
			fields: fields{
				client: mockClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.RolegroupParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Rolegroup{
					Spec: v1alpha1.RolegroupSpec{
						ForProvider: v1alpha1.RolegroupParameters{
							RolegroupName: "DEMO_ROLEGROUP",
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
			_, err := e.Delete(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Delete(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}
