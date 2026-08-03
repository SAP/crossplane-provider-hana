/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package certificate

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	certclient "github.com/SAP/crossplane-provider-hana/internal/clients/hana/certificate"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

type mockLogger struct{}

func (l *mockLogger) Debug(_ string, _ ...any)           {}
func (l *mockLogger) Info(_ string, _ ...any)            {}
func (l *mockLogger) WithValues(_ ...any) logging.Logger { return l }

type mockCertificateClient struct {
	MockRead   func(ctx context.Context, p *v1alpha1.CertificateParameters) (*v1alpha1.CertificateObservation, error)
	MockCreate func(ctx context.Context, p *v1alpha1.CertificateParameters, pem []byte) error
	MockDelete func(ctx context.Context, p *v1alpha1.CertificateParameters) error
}

func (m mockCertificateClient) Read(ctx context.Context, p *v1alpha1.CertificateParameters) (*v1alpha1.CertificateObservation, error) {
	return m.MockRead(ctx, p)
}

func (m mockCertificateClient) Create(ctx context.Context, p *v1alpha1.CertificateParameters, pem []byte) error {
	return m.MockCreate(ctx, p, pem)
}

func (m mockCertificateClient) Delete(ctx context.Context, p *v1alpha1.CertificateParameters) error {
	return m.MockDelete(ctx, p)
}

func TestConnect(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		kube      client.Client
		usage     resource.Tracker
		newClient func(db xsql.DB) certclient.Client
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
		"ErrNotCertificate": {
			reason: "An error should be returned if the managed resource is not a *Certificate",
			args:   args{mg: nil},
			want:   stderrors.New(errNotCertificate),
		},
		"ErrTrackProviderConfigUsage": {
			reason: "An error should be returned if ProviderConfig usage tracking fails",
			fields: fields{
				usage: resource.TrackerFn(func(_ context.Context, _ resource.Managed) error { return errBoom }),
			},
			args: args{mg: &v1alpha1.Certificate{}},
			want: fmt.Errorf("%s: %w", errTrackPCUsage, errBoom),
		},
		"ErrGetProviderConfig": {
			reason: "An error should be returned if the ProviderConfig cannot be fetched",
			fields: fields{
				kube:  &test.MockClient{MockGet: test.NewMockGetFn(errBoom)},
				usage: resource.TrackerFn(func(_ context.Context, _ resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{
						ResourceSpec: xpv1.ResourceSpec{
							ProviderConfigReference: &xpv1.Reference{},
						},
					},
				},
			},
			want: fmt.Errorf("%s: %w", errGetPC, errBoom),
		},
		"ErrMissingConnectionSecret": {
			reason: "An error should be returned if the ProviderConfig has no connection secret reference",
			fields: fields{
				kube:  &test.MockClient{MockGet: test.NewMockGetFn(nil)},
				usage: resource.TrackerFn(func(_ context.Context, _ resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{
						ResourceSpec: xpv1.ResourceSpec{
							ProviderConfigReference: &xpv1.Reference{},
						},
					},
				},
			},
			want: stderrors.New(errNoSecretRef),
		},
		"ErrGetConnectionSecret": {
			reason: "An error should be returned if the credentials Secret cannot be fetched",
			fields: fields{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						if pc, ok := obj.(*apisv1alpha1.ProviderConfig); ok {
							pc.Spec.Credentials.ConnectionSecretRef = &xpv1.SecretReference{}
						}
						if _, ok := obj.(*corev1.Secret); ok {
							return errBoom
						}
						return nil
					}),
				},
				usage: resource.TrackerFn(func(_ context.Context, _ resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{
						ResourceSpec: xpv1.ResourceSpec{
							ProviderConfigReference: &xpv1.Reference{},
						},
					},
				},
			},
			want: fmt.Errorf("%s: %w", errGetSecret, errBoom),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &connector{kube: tc.fields.kube, usage: tc.fields.usage, newClient: tc.fields.newClient}
			_, err := c.Connect(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nc.Connect(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestObserve(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client certclient.CertificateClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		ob  managed.ExternalObservation
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotCertificate": {
			reason: "An error should be returned if the managed resource is not a *Certificate",
			args:   args{mg: nil},
			want:   want{err: stderrors.New(errNotCertificate)},
		},
		"ErrRead": {
			reason: "An error from the certificate client Read should be returned",
			fields: fields{
				client: mockCertificateClient{
					MockRead: func(_ context.Context, _ *v1alpha1.CertificateParameters) (*v1alpha1.CertificateObservation, error) {
						return nil, errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{ForProvider: v1alpha1.CertificateParameters{Name: "my-ca"}},
				},
			},
			want: want{err: fmt.Errorf("%s: %w", errReadCert, errBoom)},
		},
		"NotFound": {
			reason: "ResourceExists should be false when Read returns nil",
			fields: fields{
				client: mockCertificateClient{
					MockRead: func(_ context.Context, _ *v1alpha1.CertificateParameters) (*v1alpha1.CertificateObservation, error) {
						return nil, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{ForProvider: v1alpha1.CertificateParameters{Name: "my-ca"}},
				},
			},
			want: want{ob: managed.ExternalObservation{ResourceExists: false}},
		},
		"Found": {
			reason: "ResourceExists and ResourceUpToDate should both be true when certificates are found",
			fields: fields{
				client: mockCertificateClient{
					MockRead: func(_ context.Context, _ *v1alpha1.CertificateParameters) (*v1alpha1.CertificateObservation, error) {
						id := 1
						return &v1alpha1.CertificateObservation{
							Certificates: []v1alpha1.ImportedCertificate{{ID: &id, Name: "my-ca-1"}},
						}, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{ForProvider: v1alpha1.CertificateParameters{Name: "my-ca"}},
				},
			},
			want: want{ob: managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{client: tc.fields.client, log: tc.fields.log}
			got, err := e.Observe(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.ob, got); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	certPEM := []byte("-----BEGIN CERTIFICATE-----\ndGVzdA==\n-----END CERTIFICATE-----\n")

	type fields struct {
		client certclient.CertificateClient
		kube   client.Client
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
		"ErrNotCertificate": {
			reason: "An error should be returned if the managed resource is not a *Certificate",
			args:   args{mg: nil},
			want:   want{err: stderrors.New(errNotCertificate)},
		},
		"ErrGetCertSecret": {
			reason: "An error should be returned if the certificate Secret cannot be fetched",
			fields: fields{
				kube: &test.MockClient{MockGet: test.NewMockGetFn(errBoom)},
				log:  &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{
						ForProvider: v1alpha1.CertificateParameters{
							Name:                 "my-ca",
							CertificateSecretRef: &xpv1.SecretKeySelector{SecretReference: xpv1.SecretReference{Namespace: "ns", Name: "secret"}, Key: "ca.crt"},
						},
					},
				},
			},
			want: want{err: fmt.Errorf("%s: %w", errGetCertSecret, fmt.Errorf("cannot get secret ns/secret: %w", errBoom))},
		},
		"ErrCreate": {
			reason: "An error from the certificate client Create should be returned",
			fields: fields{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						if s, ok := obj.(*corev1.Secret); ok {
							s.Data = map[string][]byte{"ca.crt": certPEM}
						}
						return nil
					}),
				},
				client: mockCertificateClient{
					MockCreate: func(_ context.Context, _ *v1alpha1.CertificateParameters, _ []byte) error {
						return errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{
						ForProvider: v1alpha1.CertificateParameters{
							Name:                 "my-ca",
							CertificateSecretRef: &xpv1.SecretKeySelector{SecretReference: xpv1.SecretReference{Namespace: "ns", Name: "secret"}, Key: "ca.crt"},
						},
					},
				},
			},
			want: want{err: fmt.Errorf("%s: %w", errCreateCert, errBoom)},
		},
		"Successful": {
			reason: "No error should be returned on a successful create",
			fields: fields{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						if s, ok := obj.(*corev1.Secret); ok {
							s.Data = map[string][]byte{"ca.crt": certPEM}
						}
						return nil
					}),
				},
				client: mockCertificateClient{
					MockCreate: func(_ context.Context, _ *v1alpha1.CertificateParameters, _ []byte) error {
						return nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{
						ForProvider: v1alpha1.CertificateParameters{
							Name:                 "my-ca",
							CertificateSecretRef: &xpv1.SecretKeySelector{SecretReference: xpv1.SecretReference{Namespace: "ns", Name: "secret"}, Key: "ca.crt"},
						},
					},
				},
			},
			want: want{err: nil},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{client: tc.fields.client, kube: tc.fields.kube, log: tc.fields.log}
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
		client certclient.CertificateClient
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
		"ErrNotCertificate": {
			reason: "An error should be returned if the managed resource is not a *Certificate",
			args:   args{mg: nil},
			want:   want{err: stderrors.New(errNotCertificate)},
		},
		"ErrDelete": {
			reason: "An error from the certificate client Delete should be returned",
			fields: fields{
				client: mockCertificateClient{
					MockDelete: func(_ context.Context, _ *v1alpha1.CertificateParameters) error {
						return errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{ForProvider: v1alpha1.CertificateParameters{Name: "my-ca"}},
				},
			},
			want: want{err: fmt.Errorf("%s: %w", errDeleteCert, errBoom)},
		},
		"Successful": {
			reason: "No error should be returned on a successful delete",
			fields: fields{
				client: mockCertificateClient{
					MockDelete: func(_ context.Context, _ *v1alpha1.CertificateParameters) error {
						return nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.Certificate{
					Spec: v1alpha1.CertificateSpec{ForProvider: v1alpha1.CertificateParameters{Name: "my-ca"}},
				},
			},
			want: want{err: nil},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{client: tc.fields.client, log: tc.fields.log}
			_, err := e.Delete(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Delete(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}
