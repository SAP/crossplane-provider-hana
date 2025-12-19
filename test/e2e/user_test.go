//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/privilege"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/crossplane-contrib/xp-testing/pkg/resources"
	"github.com/crossplane-contrib/xp-testing/pkg/xpenvfuncs"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	k8sresources "sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	corev1 "k8s.io/api/core/v1"
)

type UserTestConfig struct {
	TestConfig *resources.ResourceTestConfig
	Resource   *k8sresources.Resources
	Secrets    []*corev1.Secret
	Objects    []k8s.Object

	DBSchemas []string // Track schema names
	DBObjects []string // Track object names
	db        xsql.DB
}

func TestUser(t *testing.T) {
	testConfig := resources.NewResourceTestConfig(nil, "User")
	logger := logging.NewNopLogger()

	c := &UserTestConfig{
		TestConfig: testConfig,
		db:         hana.New(logger),
	}

	fB := features.New(fmt.Sprintf("%v", testConfig.Kind))
	fB.WithLabel("kind", testConfig.Kind)
	fB.Setup(c.SetupUser)

	fB.Assess("create", testConfig.AssessCreate)

	fB.Assess("update", c.assessUpdate)

	fB.Assess("delete", testConfig.AssessDelete)

	fB.Teardown(c.TeardownUser)

	testenv.Test(t, fB.Feature())
}

func (c *UserTestConfig) assessUpdate(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	for _, obj := range c.Objects {
		if err := c.Resource.Get(ctx, obj.GetName(), obj.GetNamespace(), obj); err != nil {
			t.Errorf("failed to get user: %v", err)
			return ctx
		}

		user, ok := obj.(*v1alpha1.User)
		if !ok {
			t.Errorf("failed to cast object to User: %v", obj)
			return ctx
		}

		defaultSchemaName := user.Spec.ForProvider.Username

		schemaName := c.DBSchemas[0]
		objectName := c.DBObjects[0]

		privileges := user.Spec.ForProvider.Privileges
		privileges = append(privileges,
			"AUDIT READ",
			fmt.Sprintf("CREATE ANY ON SCHEMA %s", schemaName),
			fmt.Sprintf("INSERT ON %s.%s", schemaName, objectName),
		)

		user.Spec.ForProvider.Privileges = privileges
		res := cfg.Client().Resources()

		err := res.Update(ctx, user)
		if err != nil {
			t.Fatal(err)
		}

		var fn = func(u k8s.Object) bool {
			return u.GetName() == user.GetName() && u.GetNamespace() == user.GetNamespace()
		}

		err = wait.For(
			conditions.New(res).ResourceMatch(user, fn),
		)
		if err != nil {
			t.Error(err)
		}

		privileges = append(privileges,
			privilege.GetDefaultPrivilege(defaultSchemaName),
		)

		less := func(a, b string) bool { return a < b }
		equalIgnoreOrder := cmp.Diff(privileges, user.Status.AtProvider.Privileges, cmpopts.SortSlices(less)) == ""

		if !equalIgnoreOrder {
			t.Errorf("failed to update user privileges: Status does not match Spec. Name: %s, Namespace: %s", user.Name, user.Namespace)

			out, derr := exec.Command("kubectl", "describe", "user", user.Name, "-n", user.Namespace).CombinedOutput()
			if derr != nil {
				t.Errorf("failed to run kubectl describe: %v", derr)
			} else {
				t.Logf("kubectl describe user output:\n%s", string(out))
			}
		}
	}

	return ctx
}

// Setup creates the resource and secret in the cluster
func (c *UserTestConfig) SetupUser(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Logf("Apply User")

	// Connect to database using credentials
	secretData := getProviderConfigSecretData()
	secretDataBytes := make(map[string][]byte)
	for k, v := range secretData {
		secretDataBytes[k] = []byte(v)
	}

	if err := c.db.Connect(ctx, secretDataBytes); err != nil {
		t.Errorf("failed to connect to database: %v", err)
		return ctx
	}

	resources.ImportResources(ctx, t, cfg, c.TestConfig.ResourceDirectory)

	objects := make([]k8s.Object, 0)
	err := decoder.DecodeEachFile(
		ctx, os.DirFS(c.TestConfig.ResourceDirectory), "*",
		func(ctx context.Context, obj k8s.Object) error {
			objects = append(objects, obj)
			return nil
		},
		decoder.MutateNamespace(cfg.Namespace()),
	)
	if err != nil {
		t.Errorf("failed to decode files: %v", err)
		return ctx
	}

	if c.Resource, err = k8sresources.New(cfg.Client().RESTConfig()); err != nil {
		t.Errorf("failed to create resource client: %v", err)
		return ctx
	}

	schemaName := "E2ENEWSCHEMA"
	var schemaNamePlaceholder string
	row := c.db.QueryRowContext(ctx, "SELECT SCHEMA_NAME FROM SCHEMAS WHERE SCHEMA_NAME = ?", schemaName)
	if err := row.Scan(&schemaNamePlaceholder); xsql.IsNoRows(err) {
		if _, err := c.db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName)); err != nil {
			t.Errorf("failed to create schema: %v", err)
			return ctx
		} else {
			t.Logf("created schema: %s", schemaName)
		}
	} else if err != nil {
		t.Errorf("failed to query schema: %v", err)
		return ctx
	}
	c.DBSchemas = append(c.DBSchemas, schemaName)

	objectName := "E2ENEWTABLE"
	var objectNamePlaceholder string
	row = c.db.QueryRowContext(ctx, "SELECT SCHEMA_NAME, TABLE_NAME FROM TABLES WHERE SCHEMA_NAME = ? AND TABLE_NAME = ?", schemaName, objectName)
	if err := row.Scan(&schemaNamePlaceholder, &objectNamePlaceholder); xsql.IsNoRows(err) {
		if _, err := c.db.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s.%s (ID INT PRIMARY KEY, NAME NVARCHAR(100))", schemaName, objectName)); err != nil {
			t.Errorf("failed to create table: %v", err)
			return ctx
		} else {
			t.Logf("created table: %s.%s", schemaName, objectName)
		}
	} else if err != nil {
		t.Errorf("failed to query table: %v", err)
		return ctx
	}
	c.DBObjects = append(c.DBObjects, objectName)

	for _, obj := range objects {
		user, ok := obj.(*v1alpha1.User)
		if !ok {
			t.Errorf("failed to cast object to User: %v", obj)
			return ctx
		}

		c.Objects = append(c.Objects, obj)

		secretName := user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Name
		if secretName == "" {
			t.Errorf("secret name is empty")
			return ctx
		}
		secretNamespace := user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Namespace
		if secretNamespace == "" {
			t.Errorf("secret namespace is empty")
			return ctx
		}
		secretKey := user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Key
		if secretKey == "" {
			t.Errorf("secret key is empty")
			return ctx
		}

		secret := xpenvfuncs.SimpleSecret(secretName, secretNamespace, map[string]string{
			secretKey: "Testpassword1",
		})

		if err := c.Resource.Create(ctx, secret); err != nil {
			t.Errorf("failed to create secret: %v", err)
		}

		c.Secrets = append(c.Secrets, secret)
	}

	return ctx
}

func (c *UserTestConfig) TeardownUser(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Logf("Teardown: Remove User Secrets")

	for _, secret := range c.Secrets {
		if err := c.Resource.Delete(ctx, secret); err != nil {
			t.Errorf("failed to delete secret: %v", err)
		}
	}

	if len(c.DBSchemas) > 0 && len(c.DBObjects) > 0 {
		schemaName := c.DBSchemas[0]
		objectName := c.DBObjects[0]

		if _, err := c.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE %s.%s", schemaName, objectName)); err != nil {
			t.Errorf("failed to drop table %s: %v", objectName, err)
		}

		if _, err := c.db.ExecContext(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", schemaName)); err != nil {
			t.Errorf("failed to drop schema %s: %v", schemaName, err)
		}
	}

	// Disconnect from database
	if err := c.db.Disconnect(); err != nil {
		t.Errorf("failed to disconnect from database: %v", err)
	}

	return ctx
}
