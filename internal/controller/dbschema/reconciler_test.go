/*
Copyright 2026 SAP SE.
*/

package dbschema

import (
	"context"
	"errors"
	"fmt"
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/SAP/crossplane-provider-hana/apis/schema/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/dbschema"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

// MockLogger is a mock implementation of logging.Logger
type MockLogger struct{}

// Debug logs debug messages.
func (l *MockLogger) Debug(_ string, _ ...interface{}) {}

// Info logs info messages.
func (l *MockLogger) Info(_ string, _ ...interface{}) {}

// WithValues returns a logger with the specified key-value pairs.
func (l *MockLogger) WithValues(_ ...interface{}) logging.Logger { return l }

type mockClient struct {
	MockRead   func(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) (observed *v1alpha1.DbSchemaObservation, err error)
	MockCreate func(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error
	MockDelete func(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error
}

func (m mockClient) Read(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) (observed *v1alpha1.DbSchemaObservation, err error) {
	return m.MockRead(ctx, parameters)
}

func (m mockClient) Create(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error {
	return m.MockCreate(ctx, parameters)
}

func (m mockClient) Delete(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error {
	return m.MockDelete(ctx, parameters)
}

func TestConnect(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		kube      client.Client
		usage     resource.Tracker
		newClient func(db xsql.DB) dbschema.Client
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
			reason: "An error should be returned if the managed resource is not a *Schema",
			args: args{
				mg: nil,
			},
			want: errors.New(errNotDbSchema),
		},
		"ErrTrackProviderConfigUsage": {
			reason: "An error should be returned if we can't track our ProviderConfig usage",
			fields: fields{
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return errBoom }),
			},
			args: args{
				mg: &v1alpha1.DbSchema{},
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
				mg: &v1alpha1.DbSchema{
					Spec: v1alpha1.DbSchemaSpec{
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
					// We call get to populate the Database struct, then again
					// to populate the (empty) ProviderConfig struct, resulting
					// in a ProviderConfig with a nil connection secret.
					MockGet: test.NewMockGetFn(nil),
				},
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.DbSchema{
					Spec: v1alpha1.DbSchemaSpec{
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
				mg: &v1alpha1.DbSchema{
					Spec: v1alpha1.DbSchemaSpec{
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
		client dbschema.DbSchemaClient
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
		"ErrNotSchema": {
			reason: "An error should be returned if the managed resource is not a *Schema",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotDbSchema),
			},
		},
		"ErrObserve": {
			reason: "Any errors encountered while observing the schema should be returned",
			fields: fields{
				client: mockClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) (observed *v1alpha1.DbSchemaObservation, err error) {
						return nil, errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.DbSchema{
					Spec: v1alpha1.DbSchemaSpec{
						ForProvider: v1alpha1.DbSchemaParameters{
							SchemaName: "DEMO_SCHEMA",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errSelectSchema, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully observe a schema",
			fields: fields{
				client: mockClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) (observed *v1alpha1.DbSchemaObservation, err error) {
						return &v1alpha1.DbSchemaObservation{
							SchemaName: "",
							Owner:      "",
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.DbSchema{
					Spec: v1alpha1.DbSchemaSpec{
						ForProvider: v1alpha1.DbSchemaParameters{
							SchemaName: "DEMO_SCHEMA",
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
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.c, got); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client dbschema.DbSchemaClient
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
		"ErrNotSchema": {
			reason: "An error should be returned if the managed resource is not a *Schema",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotDbSchema),
			},
		},
		"ErrCreate": {
			reason: "Any errors encountered while creating the schema should be returned",
			fields: fields{
				client: mockClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.DbSchema{
					Spec: v1alpha1.DbSchemaSpec{
						ForProvider: v1alpha1.DbSchemaParameters{
							SchemaName: "DEMO_SCHEMA",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errCreateSchema, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully create a schema",
			fields: fields{
				client: mockClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.DbSchema{
					Spec: v1alpha1.DbSchemaSpec{
						ForProvider: v1alpha1.DbSchemaParameters{
							SchemaName: "DEMO_SCHEMA",
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
		client dbschema.DbSchemaClient
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
		"ErrNotSchema": {
			reason: "An error should be returned if the managed resource is not a *Schema",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotDbSchema),
			},
		},
		"ErrDelete": {
			reason: "Any errors encountered while deleting the schema should be returned",
			fields: fields{
				client: mockClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.DbSchema{
					Spec: v1alpha1.DbSchemaSpec{
						ForProvider: v1alpha1.DbSchemaParameters{
							SchemaName: "DEMO_SCHEMA",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errDropSchema, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete a schema",
			fields: fields{
				client: mockClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.DbSchemaParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.DbSchema{
					Spec: v1alpha1.DbSchemaSpec{
						ForProvider: v1alpha1.DbSchemaParameters{
							SchemaName: "DEMO_SCHEMA",
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
