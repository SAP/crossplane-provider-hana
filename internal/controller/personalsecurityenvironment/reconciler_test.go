/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package personalsecurityenvironment

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/personalsecurityenvironment"
)

// Unlike many Kubernetes projects Crossplane does not use third party testing
// libraries, per the common Go test review comments. Crossplane encourages the
// use of table driven unit tests. The tests of the crossplane-runtime project
// are representative of the testing style Crossplane encourages.
//
// https://github.com/golang/go/wiki/TestComments
// https://github.com/crossplane/crossplane/blob/master/CONTRIBUTING.md#contributing-code

const testProvider = "test-provider"

func TestObserve(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client personalsecurityenvironment.PersonalSecurityEnvironmentClient
		kube   client.Client
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
		"ErrNotPersonalSecurityEnvironment": {
			reason: "An error should be returned if the managed resource is not a *PersonalSecurityEnvironment",
			fields: fields{
				log: &mockLogger{},
			},
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotPersonalSecurityEnvironment),
			},
		},
		"ErrRead": {
			reason: "Any errors encountered while reading the PersonalSecurityEnvironment should be returned",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error) {
						return nil, errBoom
					},
				},
				kube: &test.MockClient{},
				log:  &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
						},
					},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"PSENotExists": {
			reason: "Should return ResourceExists false when PersonalSecurityEnvironment doesn't exist",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error) {
						return nil, nil
					},
				},
				kube: &test.MockClient{},
				log:  &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nonexistent-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "nonexistent-pse",
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
			reason: "Should return ResourceUpToDate true when PersonalSecurityEnvironment is up to date",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error) {
						return &v1alpha1.PersonalSecurityEnvironmentObservation{
							Name:             "test-pse",
							X509ProviderName: testProvider,
							CertificateRefs: []v1alpha1.CertificateRef{
								{ID: new(1), Name: new("cert1")},
								{ID: new(2), Name: new("cert2")},
							},
						}, nil
					},
				},
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						if provider, ok := obj.(*v1alpha1.X509Provider); ok {
							provider.Spec.ForProvider.Name = testProvider
						}
						return nil
					}),
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
							X509ProviderRef: &v1alpha1.X509ProviderRef{
								ProviderRef: &xpv1.Reference{Name: "test-provider-ref"},
							},
							CertificateRefs: []v1alpha1.CertificateRef{
								{ID: new(1), Name: new("cert1")},
								{ID: new(2), Name: new("cert2")},
							},
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
			reason: "Should return ResourceUpToDate false when PersonalSecurityEnvironment is out of date",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error) {
						return &v1alpha1.PersonalSecurityEnvironmentObservation{
							Name:             "test-pse",
							X509ProviderName: "old-provider",
							CertificateRefs: []v1alpha1.CertificateRef{
								{ID: new(1), Name: new("cert1")},
							},
						}, nil
					},
				},
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						if provider, ok := obj.(*v1alpha1.X509Provider); ok {
							provider.Spec.ForProvider.Name = "new-provider"
						}
						return nil
					}),
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
							X509ProviderRef: &v1alpha1.X509ProviderRef{
								ProviderRef: &xpv1.Reference{Name: "new-provider-ref"},
							},
							CertificateRefs: []v1alpha1.CertificateRef{
								{ID: new(1), Name: new("cert1")},
								{ID: new(2), Name: new("cert2")},
							},
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
		"ErrGetProviderName": {
			reason: "Should return error when getting provider name fails",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error) {
						return &v1alpha1.PersonalSecurityEnvironmentObservation{
							Name:             "test-pse",
							X509ProviderName: testProvider,
						}, nil
					},
				},
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(errBoom),
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
							X509ProviderRef: &v1alpha1.X509ProviderRef{
								ProviderRef: &xpv1.Reference{Name: "test-provider-ref"},
							},
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf("failed to get provider for pse: %w", errBoom),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, kube: tc.fields.kube, log: tc.fields.log}
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
		client personalsecurityenvironment.PersonalSecurityEnvironmentClient
		kube   client.Client
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
		"ErrNotPersonalSecurityEnvironment": {
			reason: "An error should be returned if the managed resource is not a *PersonalSecurityEnvironment",
			fields: fields{
				log: &mockLogger{},
			},
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotPersonalSecurityEnvironment),
			},
		},
		"ErrGetProviderName": {
			reason: "Should return error when getting provider name fails during create",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{},
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(errBoom),
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
							X509ProviderRef: &v1alpha1.X509ProviderRef{
								ProviderRef: &xpv1.Reference{Name: "test-provider-ref"},
							},
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf("failed to get provider for pse: %w", errBoom),
			},
		},
		"ErrCreate": {
			reason: "Any errors encountered while creating the PersonalSecurityEnvironment should be returned",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters, providerName string) error {
						return errBoom
					},
				},
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						if provider, ok := obj.(*v1alpha1.X509Provider); ok {
							provider.Spec.ForProvider.Name = testProvider
						}
						return nil
					}),
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
							X509ProviderRef: &v1alpha1.X509ProviderRef{
								ProviderRef: &xpv1.Reference{Name: "test-provider-ref"},
							},
						},
					},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully create a PersonalSecurityEnvironment",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters, providerName string) error {
						return nil
					},
				},
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						if provider, ok := obj.(*v1alpha1.X509Provider); ok {
							provider.Spec.ForProvider.Name = testProvider
						}
						return nil
					}),
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
							X509ProviderRef: &v1alpha1.X509ProviderRef{
								ProviderRef: &xpv1.Reference{Name: "test-provider-ref"},
							},
						},
					},
				},
			},
			want: want{
				c: managed.ExternalCreation{},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, kube: tc.fields.kube, log: tc.fields.log}
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
		client personalsecurityenvironment.PersonalSecurityEnvironmentClient
		kube   client.Client
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
		"ErrNotPersonalSecurityEnvironment": {
			reason: "An error should be returned if the managed resource is not a *PersonalSecurityEnvironment",
			fields: fields{
				log: &mockLogger{},
			},
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotPersonalSecurityEnvironment),
			},
		},
		"ErrUpdate": {
			reason: "Any errors encountered while updating the PersonalSecurityEnvironment should be returned",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockUpdate: func(ctx context.Context, pseName string, toAdd, toRemove []v1alpha1.CertificateRef, providerName string) error {
						return errBoom
					},
				},
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						if provider, ok := obj.(*v1alpha1.X509Provider); ok {
							provider.Spec.ForProvider.Name = testProvider
						}
						return nil
					}),
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
							X509ProviderRef: &v1alpha1.X509ProviderRef{
								ProviderRef: &xpv1.Reference{Name: "test-provider-ref"},
							},
							CertificateRefs: []v1alpha1.CertificateRef{
								{ID: new(1), Name: new("cert1")},
							},
						},
					},
					Status: v1alpha1.PersonalSecurityEnvironmentStatus{
						AtProvider: v1alpha1.PersonalSecurityEnvironmentObservation{
							Name:             "test-pse",
							X509ProviderName: testProvider,
							CertificateRefs: []v1alpha1.CertificateRef{
								{ID: new(2), Name: new("cert2")},
							},
						},
					},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully update a PersonalSecurityEnvironment",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockUpdate: func(ctx context.Context, pseName string, toAdd, toRemove []v1alpha1.CertificateRef, providerName string) error {
						return nil
					},
				},
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						if provider, ok := obj.(*v1alpha1.X509Provider); ok {
							provider.Spec.ForProvider.Name = testProvider
						}
						return nil
					}),
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
							X509ProviderRef: &v1alpha1.X509ProviderRef{
								ProviderRef: &xpv1.Reference{Name: "test-provider-ref"},
							},
							CertificateRefs: []v1alpha1.CertificateRef{
								{ID: new(1), Name: new("cert1")},
							},
						},
					},
					Status: v1alpha1.PersonalSecurityEnvironmentStatus{
						AtProvider: v1alpha1.PersonalSecurityEnvironmentObservation{
							Name:             "test-pse",
							X509ProviderName: testProvider,
							CertificateRefs: []v1alpha1.CertificateRef{
								{ID: new(2), Name: new("cert2")},
							},
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
			e := external{client: tc.fields.client, kube: tc.fields.kube, log: tc.fields.log}
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
		client personalsecurityenvironment.PersonalSecurityEnvironmentClient
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
		"ErrNotPersonalSecurityEnvironment": {
			reason: "An error should be returned if the managed resource is not a *PersonalSecurityEnvironment",
			fields: fields{
				log: &mockLogger{},
			},
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotPersonalSecurityEnvironment),
			},
		},
		"ErrDelete": {
			reason: "Any errors encountered while deleting the PersonalSecurityEnvironment should be returned",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) error {
						return errBoom
					},
				},
				kube: &test.MockClient{},
				log:  &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
						},
					},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete a PersonalSecurityEnvironment",
			fields: fields{
				client: &mockPersonalSecurityEnvironmentClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) error {
						return nil
					},
				},
				kube: &test.MockClient{},
				log:  &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PersonalSecurityEnvironment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pse",
						Namespace: "default",
					},
					Spec: v1alpha1.PersonalSecurityEnvironmentSpec{
						ForProvider: v1alpha1.PersonalSecurityEnvironmentParameters{
							Name: "test-pse",
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
			e := external{client: tc.fields.client, kube: tc.fields.kube, log: tc.fields.log}
			_, err := e.Delete(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Delete(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
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

func (l *mockLogger) WithValues(_ ...any) logging.Logger { return l }

// mockPersonalSecurityEnvironmentClient implements the personalsecurityenvironment.PersonalSecurityEnvironmentClient interface for testing
type mockPersonalSecurityEnvironmentClient struct {
	MockRead   func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error)
	MockCreate func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters, providerName string) error
	MockUpdate func(ctx context.Context, pseName string, toAdd, toRemove []v1alpha1.CertificateRef, providerName string) error
	MockDelete func(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) error
}

func (m *mockPersonalSecurityEnvironmentClient) Read(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error) {
	if m.MockRead != nil {
		return m.MockRead(ctx, parameters)
	}
	return nil, nil
}

func (m *mockPersonalSecurityEnvironmentClient) Create(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters, providerName string) error {
	if m.MockCreate != nil {
		return m.MockCreate(ctx, parameters, providerName)
	}
	return nil
}

func (m *mockPersonalSecurityEnvironmentClient) Update(ctx context.Context, pseName string, toAdd, toRemove []v1alpha1.CertificateRef, providerName string) error {
	if m.MockUpdate != nil {
		return m.MockUpdate(ctx, pseName, toAdd, toRemove, providerName)
	}
	return nil
}

func (m *mockPersonalSecurityEnvironmentClient) Delete(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) error {
	if m.MockDelete != nil {
		return m.MockDelete(ctx, parameters)
	}
	return nil
}

func TestCertListDifference(t *testing.T) {
	type args struct {
		a []v1alpha1.CertificateRef
		b []v1alpha1.CertificateRef
	}

	cases := map[string]struct {
		reason string
		args   args
		want   []v1alpha1.CertificateRef
	}{
		"BothEmpty": {
			reason: "Should return empty slice when both inputs are empty",
			args: args{
				a: []v1alpha1.CertificateRef{},
				b: []v1alpha1.CertificateRef{},
			},
			want: nil,
		},
		"FirstEmpty": {
			reason: "Should return empty slice when first input is empty",
			args: args{
				a: []v1alpha1.CertificateRef{},
				b: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("cert1")},
				},
			},
			want: nil,
		},
		"SecondEmpty": {
			reason: "Should return all elements from first slice when second is empty",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("cert1")},
					{ID: new(2), Name: new("cert2")},
				},
				b: []v1alpha1.CertificateRef{},
			},
			want: []v1alpha1.CertificateRef{
				{ID: new(1), Name: new("cert1")},
				{ID: new(2), Name: new("cert2")},
			},
		},
		"BothNil": {
			reason: "Should return nil when both inputs are nil",
			args: args{
				a: nil,
				b: nil,
			},
			want: nil,
		},
		"MatchByID": {
			reason: "Should not return certificates that match by ID",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("cert1")},
					{ID: new(2), Name: new("cert2")},
				},
				b: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("different-name")},
				},
			},
			want: []v1alpha1.CertificateRef{
				{ID: new(2), Name: new("cert2")},
			},
		},
		"MatchByName": {
			reason: "Should not return certificates that match by Name",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("cert1")},
					{ID: new(2), Name: new("cert2")},
				},
				b: []v1alpha1.CertificateRef{
					{ID: new(99), Name: new("cert1")},
				},
			},
			want: []v1alpha1.CertificateRef{
				{ID: new(2), Name: new("cert2")},
			},
		},
		"MatchByIDOnly": {
			reason: "Should match by ID when only ID is set",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: new(1)},
					{ID: new(2)},
				},
				b: []v1alpha1.CertificateRef{
					{ID: new(1)},
				},
			},
			want: []v1alpha1.CertificateRef{
				{ID: new(2)},
			},
		},
		"MatchByNameOnly": {
			reason: "Should match by Name when only Name is set",
			args: args{
				a: []v1alpha1.CertificateRef{
					{Name: new("cert1")},
					{Name: new("cert2")},
				},
				b: []v1alpha1.CertificateRef{
					{Name: new("cert2")},
				},
			},
			want: []v1alpha1.CertificateRef{
				{Name: new("cert1")},
			},
		},
		"NoMatches": {
			reason: "Should return all elements when there are no matches",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("cert1")},
					{ID: new(2), Name: new("cert2")},
				},
				b: []v1alpha1.CertificateRef{
					{ID: new(3), Name: new("cert3")},
					{ID: new(4), Name: new("cert4")},
				},
			},
			want: []v1alpha1.CertificateRef{
				{ID: new(1), Name: new("cert1")},
				{ID: new(2), Name: new("cert2")},
			},
		},
		"AllMatch": {
			reason: "Should return empty slice when all elements match",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("cert1")},
					{ID: new(2), Name: new("cert2")},
				},
				b: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("cert1")},
					{ID: new(2), Name: new("cert2")},
				},
			},
			want: nil,
		},
		"PartialMatch": {
			reason: "Should return only elements that don't match",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("cert1")},
					{ID: new(2), Name: new("cert2")},
					{ID: new(3), Name: new("cert3")},
				},
				b: []v1alpha1.CertificateRef{
					{ID: new(2), Name: new("cert2")},
				},
			},
			want: []v1alpha1.CertificateRef{
				{ID: new(1), Name: new("cert1")},
				{ID: new(3), Name: new("cert3")},
			},
		},
		"EmptyNameNotMatched": {
			reason: "Should not match when Name is empty string",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("")},
				},
				b: []v1alpha1.CertificateRef{
					{ID: new(2), Name: new("")},
				},
			},
			want: []v1alpha1.CertificateRef{
				{ID: new(1), Name: new("")},
			},
		},
		"NilIDAndNilName": {
			reason: "Should not match when both ID and Name are nil",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: nil, Name: nil},
				},
				b: []v1alpha1.CertificateRef{
					{ID: nil, Name: nil},
				},
			},
			want: []v1alpha1.CertificateRef{
				{ID: nil, Name: nil},
			},
		},
		"MixedMatchingCriteria": {
			reason: "Should correctly handle mixed matching by ID and Name",
			args: args{
				a: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("cert1")},
					{ID: new(2), Name: new("cert2")},
					{ID: new(3), Name: new("cert3")},
				},
				b: []v1alpha1.CertificateRef{
					{ID: new(1), Name: new("different")}, // matches by ID
					{ID: new(99), Name: new("cert3")},    // matches by Name
				},
			},
			want: []v1alpha1.CertificateRef{
				{ID: new(2), Name: new("cert2")},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := certListDifference(tc.args.a, tc.args.b)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("\n%s\ncertListDifference(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}
