/*
Copyright 2026 SAP SE.
*/

package x509provider

import (
	"context"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/x509provider"
)

// Unlike many Kubernetes projects Crossplane does not use third party testing
// libraries, per the common Go test review comments. Crossplane encourages the
// use of table driven unit tests. The tests of the crossplane-runtime project
// are representative of the testing style Crossplane encourages.
//
// https://github.com/golang/go/wiki/TestComments
// https://github.com/crossplane/crossplane/blob/master/CONTRIBUTING.md#contributing-code

func TestObserve(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client x509provider.X509ProviderClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		o   managed.ExternalObservation
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotX509Provider": {
			reason: "An error should be returned if the managed resource is not a *X509Provider",
			fields: fields{
				log: &mockLogger{},
			},
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotX509Provider),
			},
		},
		"ErrRead": {
			reason: "Any errors encountered while reading the X509Provider should be returned",
			fields: fields{
				client: &mockX509ProviderClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) (*v1alpha1.X509ProviderObservation, error) {
						return nil, errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:   "test-provider",
							Issuer: "CN=Test CA",
						},
					},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"ProviderNotExists": {
			reason: "Should return ResourceExists false when X509Provider doesn't exist",
			fields: fields{
				client: &mockX509ProviderClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) (*v1alpha1.X509ProviderObservation, error) {
						return nil, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:   "nonexistent-provider",
							Issuer: "CN=Test CA",
						},
					},
				},
			},
			want: want{
				o: managed.ExternalObservation{
					ResourceExists: false,
				},
			},
		},
		"SuccessUpToDate": {
			reason: "Should return ResourceUpToDate true when X509Provider is up to date",
			fields: fields{
				client: &mockX509ProviderClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) (*v1alpha1.X509ProviderObservation, error) {
						return &v1alpha1.X509ProviderObservation{
							Name:          stringPtr("test-provider"),
							Issuer:        stringPtr("CN=Test CA"),
							MatchingRules: []string{"rule1", "rule2"},
						}, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:          "test-provider",
							Issuer:        "CN=Test CA",
							MatchingRules: []string{"rule1", "rule2"},
						},
					},
				},
			},
			want: want{
				o: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
				},
			},
		},
		"SuccessOutOfDate": {
			reason: "Should return ResourceUpToDate false when X509Provider is out of date",
			fields: fields{
				client: &mockX509ProviderClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) (*v1alpha1.X509ProviderObservation, error) {
						return &v1alpha1.X509ProviderObservation{
							Name:          stringPtr("test-provider"),
							Issuer:        stringPtr("CN=Old CA"),
							MatchingRules: []string{"old-rule"},
						}, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:          "test-provider",
							Issuer:        "CN=New CA",
							MatchingRules: []string{"new-rule"},
						},
					},
				},
			},
			want: want{
				o: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: false,
				},
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
			if diff := cmp.Diff(tc.want.o, got); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client x509provider.X509ProviderClient
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
		"ErrNotX509Provider": {
			reason: "An error should be returned if the managed resource is not a *X509Provider",
			fields: fields{
				log: &mockLogger{},
			},
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotX509Provider),
			},
		},
		"ErrCreate": {
			reason: "Any errors encountered while creating the X509Provider should be returned",
			fields: fields{
				client: &mockX509ProviderClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error {
						return errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:   "test-provider",
							Issuer: "CN=Test CA",
						},
					},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully create an X509Provider",
			fields: fields{
				client: &mockX509ProviderClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error {
						return nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:   "test-provider",
							Issuer: "CN=Test CA",
						},
					},
				},
			},
			want: want{
				c: managed.ExternalCreation{
					ConnectionDetails: managed.ConnectionDetails{},
				},
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

func TestUpdate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client x509provider.X509ProviderClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		u   managed.ExternalUpdate
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotX509Provider": {
			reason: "An error should be returned if the managed resource is not a *X509Provider",
			fields: fields{
				log: &mockLogger{},
			},
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotX509Provider),
			},
		},
		"ErrUpdate": {
			reason: "Any errors encountered while updating the X509Provider should be returned",
			fields: fields{
				client: &mockX509ProviderClient{
					MockUpdate: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters, observation *v1alpha1.X509ProviderObservation) error {
						return errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:   "test-provider",
							Issuer: "CN=New CA",
						},
					},
					Status: v1alpha1.X509ProviderStatus{
						AtProvider: v1alpha1.X509ProviderObservation{
							Name:   stringPtr("test-provider"),
							Issuer: stringPtr("CN=Old CA"),
						},
					},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully update an X509Provider",
			fields: fields{
				client: &mockX509ProviderClient{
					MockUpdate: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters, observation *v1alpha1.X509ProviderObservation) error {
						return nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:   "test-provider",
							Issuer: "CN=New CA",
						},
					},
					Status: v1alpha1.X509ProviderStatus{
						AtProvider: v1alpha1.X509ProviderObservation{
							Name:   stringPtr("test-provider"),
							Issuer: stringPtr("CN=Old CA"),
						},
					},
				},
			},
			want: want{
				u: managed.ExternalUpdate{
					ConnectionDetails: managed.ConnectionDetails{},
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			got, err := e.Update(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Update(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.u, got); diff != "" {
				t.Errorf("\n%s\ne.Update(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client x509provider.X509ProviderClient
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
		"ErrNotX509Provider": {
			reason: "An error should be returned if the managed resource is not a *X509Provider",
			fields: fields{
				log: &mockLogger{},
			},
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotX509Provider),
			},
		},
		"ErrDelete": {
			reason: "Any errors encountered while deleting the X509Provider should be returned",
			fields: fields{
				client: &mockX509ProviderClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error {
						return errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:   "test-provider",
							Issuer: "CN=Test CA",
						},
					},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete an X509Provider",
			fields: fields{
				client: &mockX509ProviderClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error {
						return nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.X509Provider{
					Spec: v1alpha1.X509ProviderSpec{
						ForProvider: v1alpha1.X509ProviderParameters{
							Name:   "test-provider",
							Issuer: "CN=Test CA",
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

// Helper functions for testing
func stringPtr(s string) *string {
	return &s
}

// mockLogger is a mock implementation of logging.Logger
type mockLogger struct {
	msgs []string
}

func (l *mockLogger) Debug(msg string, keysAndValues ...any) {
	l.msgs = append(l.msgs, msg)
}

func (l *mockLogger) Info(msg string, keysAndValues ...any) {
	l.msgs = append(l.msgs, msg)
}

func (l *mockLogger) WithValues(_ ...interface{}) logging.Logger { return l }

// mockX509ProviderClient implements the x509provider.X509ProviderClient interface for testing
type mockX509ProviderClient struct {
	MockRead   func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) (*v1alpha1.X509ProviderObservation, error)
	MockCreate func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error
	MockUpdate func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters, observation *v1alpha1.X509ProviderObservation) error
	MockDelete func(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error
}

func (m *mockX509ProviderClient) Read(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) (*v1alpha1.X509ProviderObservation, error) {
	if m.MockRead != nil {
		return m.MockRead(ctx, parameters)
	}
	return nil, nil
}

func (m *mockX509ProviderClient) Create(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error {
	if m.MockCreate != nil {
		return m.MockCreate(ctx, parameters)
	}
	return nil
}

func (m *mockX509ProviderClient) Update(ctx context.Context, parameters *v1alpha1.X509ProviderParameters, observation *v1alpha1.X509ProviderObservation) error {
	if m.MockUpdate != nil {
		return m.MockUpdate(ctx, parameters, observation)
	}
	return nil
}

func (m *mockX509ProviderClient) Delete(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error {
	if m.MockDelete != nil {
		return m.MockDelete(ctx, parameters)
	}
	return nil
}
