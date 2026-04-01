/*
Copyright 2026 SAP SE.
*/

package instancemapping

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"

	"github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	imclient "github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud/instancemapping"
)

const testNamespace = "test-namespace"

// MockLogger is a mock implementation of logging.Logger
type MockLogger struct{}

func (l *MockLogger) Debug(_ string, _ ...interface{}) {}
func (l *MockLogger) Info(_ string, _ ...interface{})  {}
func (l *MockLogger) WithValues(_ ...interface{}) logging.Logger {
	return l
}

// mockInstanceMappingClient mocks the instancemapping.Client interface
type mockInstanceMappingClient struct {
	MockList   func(ctx context.Context, serviceInstanceID string) ([]imclient.InstanceMapping, error)
	MockCreate func(ctx context.Context, serviceInstanceID string, req imclient.CreateMappingRequest) error
	MockDelete func(ctx context.Context, serviceInstanceID, primaryID, secondaryID string) error
}

func (m *mockInstanceMappingClient) List(ctx context.Context, serviceInstanceID string) ([]imclient.InstanceMapping, error) {
	return m.MockList(ctx, serviceInstanceID)
}

func (m *mockInstanceMappingClient) Create(ctx context.Context, serviceInstanceID string, req imclient.CreateMappingRequest) error {
	return m.MockCreate(ctx, serviceInstanceID, req)
}

func (m *mockInstanceMappingClient) Delete(ctx context.Context, serviceInstanceID, primaryID, secondaryID string) error {
	return m.MockDelete(ctx, serviceInstanceID, primaryID, secondaryID)
}

func TestObserve(t *testing.T) {
	errBoom := errors.New("boom")
	secondaryID := testNamespace

	type fields struct {
		client imclient.Client
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
		"ErrNotInstanceMapping": {
			reason: "An error should be returned if the managed resource is not an *InstanceMapping",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotInstanceMapping),
			},
		},
		"MappingExistsWithBothIDs": {
			reason: "ResourceExists should be true when mapping with both IDs is found",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockList: func(ctx context.Context, serviceInstanceID string) ([]imclient.InstanceMapping, error) {
						return []imclient.InstanceMapping{
							{
								Platform:    "kubernetes",
								PrimaryID:   "cluster-1",
								SecondaryID: &secondaryID,
								IsDefault:   true,
							},
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
							SecondaryID:       &secondaryID,
							IsDefault:         true,
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
		"MappingExistsPrimaryOnly": {
			reason: "ResourceExists should be true when mapping matches with nil secondaryID on both sides",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockList: func(ctx context.Context, serviceInstanceID string) ([]imclient.InstanceMapping, error) {
						return []imclient.InstanceMapping{
							{
								Platform:    "kubernetes",
								PrimaryID:   "cluster-1",
								SecondaryID: nil,
								IsDefault:   false,
							},
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
							SecondaryID:       nil,
							IsDefault:         false,
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
		"MappingNotFoundEmptyList": {
			reason: "ResourceExists should be false when list returns empty",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockList: func(ctx context.Context, serviceInstanceID string) ([]imclient.InstanceMapping, error) {
						return []imclient.InstanceMapping{}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
							SecondaryID:       &secondaryID,
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
		"MappingPartialMatch": {
			reason: "ResourceExists should be false when only primaryID matches but secondaryID differs",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockList: func(ctx context.Context, serviceInstanceID string) ([]imclient.InstanceMapping, error) {
						differentSecondaryID := "different-namespace"
						return []imclient.InstanceMapping{
							{
								Platform:    "kubernetes",
								PrimaryID:   "cluster-1",
								SecondaryID: &differentSecondaryID,
								IsDefault:   true,
							},
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
							SecondaryID:       &secondaryID,
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
		"ErrListMappings": {
			reason: "Any errors encountered while listing mappings should be returned",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockList: func(ctx context.Context, serviceInstanceID string) ([]imclient.InstanceMapping, error) {
						return nil, errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errListMappings, errBoom),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{client: tc.fields.client, log: tc.fields.log}
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
	secondaryID := testNamespace

	type fields struct {
		client imclient.Client
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
		"ErrNotInstanceMapping": {
			reason: "An error should be returned if the managed resource is not an *InstanceMapping",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotInstanceMapping),
			},
		},
		"SuccessWithAllFields": {
			reason: "No error should be returned when we successfully create a mapping with all fields",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockCreate: func(ctx context.Context, serviceInstanceID string, req imclient.CreateMappingRequest) error {
						if serviceInstanceID != "test-instance-id" {
							t.Errorf("expected serviceInstanceID 'test-instance-id', got %s", serviceInstanceID)
						}
						if req.Platform != "kubernetes" {
							t.Errorf("expected platform 'kubernetes', got %s", req.Platform)
						}
						if req.PrimaryID != "cluster-1" {
							t.Errorf("expected primaryID 'cluster-1', got %s", req.PrimaryID)
						}
						if req.SecondaryID == nil || *req.SecondaryID != testNamespace {
							t.Errorf("expected secondaryID 'test-namespace', got %v", req.SecondaryID)
						}
						if !req.IsDefault {
							t.Error("expected isDefault to be true")
						}
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
							SecondaryID:       &secondaryID,
							IsDefault:         true,
						},
					},
				},
			},
			want: want{},
		},
		"SuccessWithNilSecondaryID": {
			reason: "No error should be returned when secondaryID is nil",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockCreate: func(ctx context.Context, serviceInstanceID string, req imclient.CreateMappingRequest) error {
						if req.SecondaryID != nil {
							t.Errorf("expected nil secondaryID, got %v", req.SecondaryID)
						}
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
							SecondaryID:       nil,
						},
					},
				},
			},
			want: want{},
		},
		"ErrCreateMapping": {
			reason: "Any errors encountered while creating the mapping should be returned",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockCreate: func(ctx context.Context, serviceInstanceID string, req imclient.CreateMappingRequest) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errCreateMapping, errBoom),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{client: tc.fields.client, log: tc.fields.log}
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
		args   args
		want   want
	}{
		"NoOp": {
			reason: "Instance mappings are immutable, Update should be a no-op",
			args: args{
				mg: &v1alpha1.InstanceMapping{},
			},
			want: want{
				u:   managed.ExternalUpdate{},
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{}
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
	secondaryID := testNamespace

	type fields struct {
		client imclient.Client
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
		"ErrNotInstanceMapping": {
			reason: "An error should be returned if the managed resource is not an *InstanceMapping",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotInstanceMapping),
			},
		},
		"SuccessWithBothIDs": {
			reason: "No error should be returned when we successfully delete a mapping with both IDs",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockDelete: func(ctx context.Context, serviceInstanceID, primaryID, secondaryIDParam string) error {
						if serviceInstanceID != "test-instance-id" {
							t.Errorf("expected serviceInstanceID 'test-instance-id', got %s", serviceInstanceID)
						}
						if primaryID != "cluster-1" {
							t.Errorf("expected primaryID 'cluster-1', got %s", primaryID)
						}
						if secondaryIDParam != testNamespace {
							t.Errorf("expected secondaryID 'test-namespace', got %s", secondaryIDParam)
						}
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
							SecondaryID:       &secondaryID,
						},
					},
				},
			},
			want: want{},
		},
		"SuccessWithNilSecondaryID": {
			reason: "No error should be returned when secondaryID is nil (should pass empty string)",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockDelete: func(ctx context.Context, serviceInstanceID, primaryID, secondaryIDParam string) error {
						if secondaryIDParam != "" {
							t.Errorf("expected empty secondaryID, got %s", secondaryIDParam)
						}
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
							SecondaryID:       nil,
						},
					},
				},
			},
			want: want{},
		},
		"ErrDeleteMapping": {
			reason: "Any errors encountered while deleting the mapping should be returned",
			fields: fields{
				client: &mockInstanceMappingClient{
					MockDelete: func(ctx context.Context, serviceInstanceID, primaryID, secondaryIDParam string) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.InstanceMapping{
					Spec: v1alpha1.InstanceMappingSpec{
						ForProvider: v1alpha1.InstanceMappingParameters{
							ServiceInstanceID: "test-instance-id",
							Platform:          "kubernetes",
							PrimaryID:         "cluster-1",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errDeleteMapping, errBoom),
			},
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

func TestStringPtrEqual(t *testing.T) {
	x := "x"
	y := "y"

	cases := map[string]struct {
		a    *string
		b    *string
		want bool
	}{
		"BothNil": {
			a:    nil,
			b:    nil,
			want: true,
		},
		"FirstNil": {
			a:    nil,
			b:    &x,
			want: false,
		},
		"SecondNil": {
			a:    &x,
			b:    nil,
			want: false,
		},
		"Equal": {
			a:    &x,
			b:    &x,
			want: true,
		},
		"NotEqual": {
			a:    &x,
			b:    &y,
			want: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := stringPtrEqual(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("stringPtrEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
