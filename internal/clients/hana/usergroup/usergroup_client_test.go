package usergroup

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
)

func TestRead(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.UsergroupParameters
	}

	type want struct {
		observed *v1alpha1.UsergroupObservation
		err      error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrRead": {
			reason: "Any errors encountered while reading the user group should be returned",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						mock.ExpectQuery("SELECT").WillReturnError(errBoom)
						return db.QueryRow("SELECT")
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UsergroupParameters{
					UsergroupName: "DEMO_USERGROUP",
				},
			},
			want: want{
				observed: &v1alpha1.UsergroupObservation{
					UsergroupName: "",
					Parameters:    make(map[string]string),
				},
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully read a user group",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"USERGROUP_NAME", "IS_USER_ADMIN_ENABLED"}).
							AddRow("DEMO_GROUP", "TRUE")
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRow("SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						return fake.MockRowsToSQLRows(
							sqlmock.NewRows(
								[]string{"USERGROUP_NAME", "PARAMETER_KEY", "PARAMETER_VALUE"},
							).AddRow("DEMO_USERGROUP", "key", "value"),
						), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UsergroupParameters{
					UsergroupName: "",
					Parameters: map[string]string{
						"key": "value",
					},
				},
			},
			want: want{
				observed: &v1alpha1.UsergroupObservation{
					UsergroupName: "DEMO_GROUP",
					Parameters: map[string]string{
						"key": "value",
					},
				},
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
			got, err := c.Read(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.observed, got); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.UsergroupParameters
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
		"ErrCreate": {
			reason: "Any errors encountered while creating the usergroup should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UsergroupParameters{
					UsergroupName: "DEMO_USERGROUP",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully create a usergroup",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UsergroupParameters{
					UsergroupName: "DEMO_USERGROUP",
				},
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
			err := c.Create(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.UsergroupParameters
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
		"ErrDelete": {
			reason: "Any errors encountered while deleting the usergroup should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UsergroupParameters{
					UsergroupName: "DEMO_USERGROUP",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete a usergroup",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UsergroupParameters{
					UsergroupName: "DEMO_USERGROUP",
				},
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
			err := c.Delete(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}
