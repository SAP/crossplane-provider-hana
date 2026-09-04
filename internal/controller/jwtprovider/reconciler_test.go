/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package jwtprovider

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
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/jwtprovider"
)

func TestObserve(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client jwtprovider.JWTProviderClient
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
		"ErrNotJWTProvider": {
			reason: "An error should be returned if the managed resource is not a *JWTProvider",
			fields: fields{log: &mockLogger{}},
			args:   args{mg: nil},
			want:   want{err: errors.New(errNotJWTProvider)},
		},
		"ErrRead": {
			reason: "Any errors encountered while reading the JWTProvider should be returned",
			fields: fields{
				client: &mockJWTProviderClient{
					MockRead: func(_ context.Context, _ *v1alpha1.JWTProviderParameters) (*v1alpha1.JWTProviderObservation, error) {
						return nil, errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{Name: "P", Issuer: "https://issuer.example", Priority: 100},
					},
				},
			},
			want: want{err: errBoom},
		},
		"ProviderNotExists": {
			reason: "Should return ResourceExists false when the JWTProvider does not exist in HANA",
			fields: fields{
				client: &mockJWTProviderClient{
					MockRead: func(_ context.Context, _ *v1alpha1.JWTProviderParameters) (*v1alpha1.JWTProviderObservation, error) {
						return nil, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{Name: "P", Issuer: "https://issuer.example", Priority: 100},
					},
				},
			},
			want: want{o: managed.ExternalObservation{ResourceExists: false}},
		},
		"SuccessUpToDate": {
			reason: "Should return ResourceUpToDate true when the JWTProvider is in sync",
			fields: fields{
				client: &mockJWTProviderClient{
					MockRead: func(_ context.Context, _ *v1alpha1.JWTProviderParameters) (*v1alpha1.JWTProviderObservation, error) {
						issuer := "https://issuer.example"
						claim := "sub"
						prio := 100
						return &v1alpha1.JWTProviderObservation{
							Name:                  new("P"),
							Issuer:                &issuer,
							ExternalIdentityClaim: &claim,
							Priority:              &prio,
						}, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{
							Name:                  "P",
							Issuer:                "https://issuer.example",
							ExternalIdentityClaim: "sub",
							Priority:              100,
						},
					},
				},
			},
			want: want{o: managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}},
		},
		"SuccessOutOfDate": {
			reason: "Should return ResourceUpToDate false when the issuer has drifted",
			fields: fields{
				client: &mockJWTProviderClient{
					MockRead: func(_ context.Context, _ *v1alpha1.JWTProviderParameters) (*v1alpha1.JWTProviderObservation, error) {
						oldIssuer := "https://old.example"
						claim := "sub"
						prio := 100
						return &v1alpha1.JWTProviderObservation{
							Name:                  new("P"),
							Issuer:                &oldIssuer,
							ExternalIdentityClaim: &claim,
							Priority:              &prio,
						}, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{
							Name:     "P",
							Issuer:   "https://new.example",
							Priority: 100,
						},
					},
				},
			},
			want: want{o: managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: false}},
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
		client jwtprovider.JWTProviderClient
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
		"ErrNotJWTProvider": {
			reason: "An error should be returned if the managed resource is not a *JWTProvider",
			fields: fields{log: &mockLogger{}},
			args:   args{mg: nil},
			want:   want{err: errors.New(errNotJWTProvider)},
		},
		"ErrCreate": {
			reason: "Any errors encountered while creating the JWTProvider should be returned",
			fields: fields{
				client: &mockJWTProviderClient{
					MockCreate: func(_ context.Context, _ *v1alpha1.JWTProviderParameters) error {
						return errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{Name: "P", Issuer: "https://issuer.example", Priority: 100},
					},
				},
			},
			want: want{err: errBoom},
		},
		"Success": {
			reason: "No error should be returned when we successfully create a JWTProvider",
			fields: fields{
				client: &mockJWTProviderClient{
					MockCreate: func(_ context.Context, _ *v1alpha1.JWTProviderParameters) error { return nil },
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{Name: "P", Issuer: "https://issuer.example", Priority: 100},
					},
				},
			},
			want: want{c: managed.ExternalCreation{ConnectionDetails: managed.ConnectionDetails{}}},
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
		client jwtprovider.JWTProviderClient
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
		"ErrNotJWTProvider": {
			reason: "An error should be returned if the managed resource is not a *JWTProvider",
			fields: fields{log: &mockLogger{}},
			args:   args{mg: nil},
			want:   want{err: errors.New(errNotJWTProvider)},
		},
		"ErrUpdate": {
			reason: "Any errors encountered while updating the JWTProvider should be returned",
			fields: fields{
				client: &mockJWTProviderClient{
					MockUpdate: func(_ context.Context, _ *v1alpha1.JWTProviderParameters, _ *v1alpha1.JWTProviderObservation) error {
						return errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{Name: "P", Issuer: "https://new.example", Priority: 100},
					},
				},
			},
			want: want{err: errBoom},
		},
		"Success": {
			reason: "No error should be returned when we successfully update a JWTProvider",
			fields: fields{
				client: &mockJWTProviderClient{
					MockUpdate: func(_ context.Context, _ *v1alpha1.JWTProviderParameters, _ *v1alpha1.JWTProviderObservation) error {
						return nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{Name: "P", Issuer: "https://new.example", Priority: 100},
					},
				},
			},
			want: want{u: managed.ExternalUpdate{ConnectionDetails: managed.ConnectionDetails{}}},
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
		client jwtprovider.JWTProviderClient
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
		"ErrNotJWTProvider": {
			reason: "An error should be returned if the managed resource is not a *JWTProvider",
			fields: fields{log: &mockLogger{}},
			args:   args{mg: nil},
			want:   want{err: errors.New(errNotJWTProvider)},
		},
		"ErrDelete": {
			reason: "Any errors encountered while deleting the JWTProvider should be returned",
			fields: fields{
				client: &mockJWTProviderClient{
					MockDelete: func(_ context.Context, _ *v1alpha1.JWTProviderParameters) error { return errBoom },
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{Name: "P", Issuer: "https://issuer.example", Priority: 100},
					},
				},
			},
			want: want{err: errBoom},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete a JWTProvider",
			fields: fields{
				client: &mockJWTProviderClient{
					MockDelete: func(_ context.Context, _ *v1alpha1.JWTProviderParameters) error { return nil },
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.JWTProvider{
					Spec: v1alpha1.JWTProviderSpec{
						ForProvider: v1alpha1.JWTProviderParameters{Name: "P", Issuer: "https://issuer.example", Priority: 100},
					},
				},
			},
			want: want{err: nil},
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

type mockLogger struct{ msgs []string }

func (l *mockLogger) Debug(msg string, _ ...any)         { l.msgs = append(l.msgs, msg) }
func (l *mockLogger) Info(msg string, _ ...any)          { l.msgs = append(l.msgs, msg) }
func (l *mockLogger) WithValues(_ ...any) logging.Logger { return l }

type mockJWTProviderClient struct {
	MockRead   func(ctx context.Context, parameters *v1alpha1.JWTProviderParameters) (*v1alpha1.JWTProviderObservation, error)
	MockCreate func(ctx context.Context, parameters *v1alpha1.JWTProviderParameters) error
	MockUpdate func(ctx context.Context, parameters *v1alpha1.JWTProviderParameters, observation *v1alpha1.JWTProviderObservation) error
	MockDelete func(ctx context.Context, parameters *v1alpha1.JWTProviderParameters) error
}

func (m *mockJWTProviderClient) Read(ctx context.Context, p *v1alpha1.JWTProviderParameters) (*v1alpha1.JWTProviderObservation, error) {
	if m.MockRead != nil {
		return m.MockRead(ctx, p)
	}
	return nil, nil
}

func (m *mockJWTProviderClient) Create(ctx context.Context, p *v1alpha1.JWTProviderParameters) error {
	if m.MockCreate != nil {
		return m.MockCreate(ctx, p)
	}
	return nil
}

func (m *mockJWTProviderClient) Update(ctx context.Context, p *v1alpha1.JWTProviderParameters, o *v1alpha1.JWTProviderObservation) error {
	if m.MockUpdate != nil {
		return m.MockUpdate(ctx, p, o)
	}
	return nil
}

func (m *mockJWTProviderClient) Delete(ctx context.Context, p *v1alpha1.JWTProviderParameters) error {
	if m.MockDelete != nil {
		return m.MockDelete(ctx, p)
	}
	return nil
}
