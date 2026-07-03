/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package publickey

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
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/publickey"
)

func TestObserve(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client publickey.PublicKeyClient
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
		"ErrNotPublicKey": {
			reason: "An error should be returned if the managed resource is not a *PublicKey",
			fields: fields{log: &mockLogger{}},
			args:   args{mg: nil},
			want:   want{err: errors.New(errNotPublicKey)},
		},
		"ErrRead": {
			reason: "Any errors encountered while reading the PublicKey should be returned",
			fields: fields{
				client: &mockPublicKeyClient{
					MockRead: func(_ context.Context, _ *v1alpha1.PublicKeyParameters) (*v1alpha1.PublicKeyObservation, error) {
						return nil, errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy"},
					},
				},
			},
			want: want{err: errBoom},
		},
		"KeyNotExists": {
			reason: "Should return ResourceExists false when the PublicKey does not exist in HANA",
			fields: fields{
				client: &mockPublicKeyClient{
					MockRead: func(_ context.Context, _ *v1alpha1.PublicKeyParameters) (*v1alpha1.PublicKeyObservation, error) {
						return nil, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy"},
					},
				},
			},
			want: want{o: managed.ExternalObservation{ResourceExists: false}},
		},
		"SuccessUpToDate": {
			reason: "Should return ResourceUpToDate true when the comment matches",
			fields: fields{
				client: &mockPublicKeyClient{
					MockRead: func(_ context.Context, _ *v1alpha1.PublicKeyParameters) (*v1alpha1.PublicKeyObservation, error) {
						comment := "my key"
						return &v1alpha1.PublicKeyObservation{
							Name:    new("K"),
							Comment: &comment,
						}, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy", Comment: "my key"},
					},
				},
			},
			want: want{o: managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}},
		},
		"SuccessOutOfDate": {
			reason: "Should return ResourceUpToDate false when the comment has drifted",
			fields: fields{
				client: &mockPublicKeyClient{
					MockRead: func(_ context.Context, _ *v1alpha1.PublicKeyParameters) (*v1alpha1.PublicKeyObservation, error) {
						old := "old comment"
						return &v1alpha1.PublicKeyObservation{
							Name:    new("K"),
							Comment: &old,
						}, nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy", Comment: "new comment"},
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
		client publickey.PublicKeyClient
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
		"ErrNotPublicKey": {
			reason: "An error should be returned if the managed resource is not a *PublicKey",
			fields: fields{log: &mockLogger{}},
			args:   args{mg: nil},
			want:   want{err: errors.New(errNotPublicKey)},
		},
		"ErrCreate": {
			reason: "Any errors encountered while creating the PublicKey should be returned",
			fields: fields{
				client: &mockPublicKeyClient{
					MockCreate: func(_ context.Context, _ *v1alpha1.PublicKeyParameters) error { return errBoom },
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy"},
					},
				},
			},
			want: want{err: errBoom},
		},
		"Success": {
			reason: "No error should be returned when we successfully create a PublicKey",
			fields: fields{
				client: &mockPublicKeyClient{
					MockCreate: func(_ context.Context, _ *v1alpha1.PublicKeyParameters) error { return nil },
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy"},
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
		client publickey.PublicKeyClient
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
		"ErrNotPublicKey": {
			reason: "An error should be returned if the managed resource is not a *PublicKey",
			fields: fields{log: &mockLogger{}},
			args:   args{mg: nil},
			want:   want{err: errors.New(errNotPublicKey)},
		},
		"ErrUpdate": {
			reason: "Any errors encountered while updating the PublicKey should be returned",
			fields: fields{
				client: &mockPublicKeyClient{
					MockUpdate: func(_ context.Context, _ *v1alpha1.PublicKeyParameters, _ *v1alpha1.PublicKeyObservation) error {
						return errBoom
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy", Comment: "new"},
					},
					Status: v1alpha1.PublicKeyStatus{
						AtProvider: v1alpha1.PublicKeyObservation{Comment: new("old")},
					},
				},
			},
			want: want{err: errBoom},
		},
		"Success": {
			reason: "No error should be returned when we successfully update a PublicKey",
			fields: fields{
				client: &mockPublicKeyClient{
					MockUpdate: func(_ context.Context, _ *v1alpha1.PublicKeyParameters, _ *v1alpha1.PublicKeyObservation) error {
						return nil
					},
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy", Comment: "new"},
					},
					Status: v1alpha1.PublicKeyStatus{
						AtProvider: v1alpha1.PublicKeyObservation{Comment: new("old")},
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
		client publickey.PublicKeyClient
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
		"ErrNotPublicKey": {
			reason: "An error should be returned if the managed resource is not a *PublicKey",
			fields: fields{log: &mockLogger{}},
			args:   args{mg: nil},
			want:   want{err: errors.New(errNotPublicKey)},
		},
		"ErrDelete": {
			reason: "Any errors encountered while deleting the PublicKey should be returned",
			fields: fields{
				client: &mockPublicKeyClient{
					MockDelete: func(_ context.Context, _ *v1alpha1.PublicKeyParameters) error { return errBoom },
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy"},
					},
				},
			},
			want: want{err: errBoom},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete a PublicKey",
			fields: fields{
				client: &mockPublicKeyClient{
					MockDelete: func(_ context.Context, _ *v1alpha1.PublicKeyParameters) error { return nil },
				},
				log: &mockLogger{},
			},
			args: args{
				mg: &v1alpha1.PublicKey{
					Spec: v1alpha1.PublicKeySpec{
						ForProvider: v1alpha1.PublicKeyParameters{Name: "K", PEM: "dummy"},
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

type mockPublicKeyClient struct {
	MockRead   func(ctx context.Context, parameters *v1alpha1.PublicKeyParameters) (*v1alpha1.PublicKeyObservation, error)
	MockCreate func(ctx context.Context, parameters *v1alpha1.PublicKeyParameters) error
	MockUpdate func(ctx context.Context, parameters *v1alpha1.PublicKeyParameters, observation *v1alpha1.PublicKeyObservation) error
	MockDelete func(ctx context.Context, parameters *v1alpha1.PublicKeyParameters) error
}

func (m *mockPublicKeyClient) Read(ctx context.Context, p *v1alpha1.PublicKeyParameters) (*v1alpha1.PublicKeyObservation, error) {
	if m.MockRead != nil {
		return m.MockRead(ctx, p)
	}
	return nil, nil
}

func (m *mockPublicKeyClient) Create(ctx context.Context, p *v1alpha1.PublicKeyParameters) error {
	if m.MockCreate != nil {
		return m.MockCreate(ctx, p)
	}
	return nil
}

func (m *mockPublicKeyClient) Update(ctx context.Context, p *v1alpha1.PublicKeyParameters, o *v1alpha1.PublicKeyObservation) error {
	if m.MockUpdate != nil {
		return m.MockUpdate(ctx, p, o)
	}
	return nil
}

func (m *mockPublicKeyClient) Delete(ctx context.Context, p *v1alpha1.PublicKeyParameters) error {
	if m.MockDelete != nil {
		return m.MockDelete(ctx, p)
	}
	return nil
}
