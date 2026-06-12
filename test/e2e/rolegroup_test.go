//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/crossplane-contrib/xp-testing/pkg/resources"
	"github.com/crossplane-contrib/xp-testing/pkg/xpconditions"
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	k8sresources "sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

type RolegroupTestConfig struct {
	TestConfig     *resources.ResourceTestConfig
	Resource       *k8sresources.Resources
	Objects        []k8s.Object
	RolegroupNames []string
	RoleNames      []string
	db             xsql.Connector
	conn           xsql.DB
}

func (c *RolegroupTestConfig) connectDB(ctx context.Context, t *testing.T) {
	secretData := getProviderConfigSecretData()
	secretDataBytes := make(map[string][]byte)
	for k, v := range secretData {
		secretDataBytes[k] = []byte(v)
	}
	conn, err := c.db.Connect(ctx, secretDataBytes)
	if err != nil {
		t.Fatalf("failed to connect to database: %v", err)
	}
	c.conn = conn
}

func (c *RolegroupTestConfig) SetupRolegroup(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Log("Apply Rolegroup")
	c.connectDB(ctx, t)

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
		t.Fatalf("failed to decode files: %v", err)
	}

	if c.Resource, err = k8sresources.New(cfg.Client().RESTConfig()); err != nil {
		t.Fatalf("failed to create resource client: %v", err)
	}

	for _, obj := range objects {
		rg, ok := obj.(*v1alpha1.Rolegroup)
		if !ok {
			continue
		}
		c.Objects = append(c.Objects, obj)
		c.RolegroupNames = append(c.RolegroupNames, rg.Spec.ForProvider.RolegroupName)
	}

	return ctx
}

func (c *RolegroupTestConfig) TeardownRolegroup(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Log("Teardown: Clean up rolegroups")
	for _, name := range c.RoleNames {
		_, _ = c.conn.ExecContext(ctx, fmt.Sprintf(`DROP ROLE "%s"`, name))
	}
	for _, name := range c.RolegroupNames {
		_, _ = c.conn.ExecContext(ctx, fmt.Sprintf(`DROP ROLEGROUP "%s"`, name))
	}
	if err := c.db.Disconnect(); err != nil {
		t.Errorf("failed to disconnect from database: %v", err)
	}
	return ctx
}

func (c *RolegroupTestConfig) verifyRolegroupExistsInDB(ctx context.Context, t *testing.T, rolegroupName string) {
	var name string
	row := c.conn.QueryRowContext(ctx, "SELECT ROLEGROUP_NAME FROM SYS.ROLEGROUPS WHERE ROLEGROUP_NAME = ?", rolegroupName)
	if err := row.Scan(&name); err != nil {
		t.Errorf("rolegroup %q not found in SYS.ROLEGROUPS: %v", rolegroupName, err)
		return
	}
	t.Logf("Verified rolegroup %q exists in HANA DB", rolegroupName)
}

func (c *RolegroupTestConfig) verifyRolegroupNotExistsInDB(ctx context.Context, t *testing.T, rolegroupName string) {
	for i := 0; i < 6; i++ {
		var name string
		row := c.conn.QueryRowContext(ctx, "SELECT ROLEGROUP_NAME FROM SYS.ROLEGROUPS WHERE ROLEGROUP_NAME = ?", rolegroupName)
		if err := row.Scan(&name); xsql.IsNoRows(err) {
			t.Logf("Verified rolegroup %q does not exist in HANA DB", rolegroupName)
			return
		} else if err != nil {
			t.Errorf("unexpected error checking rolegroup %q: %v", rolegroupName, err)
			return
		}
		if i < 5 {
			time.Sleep(5 * time.Second)
		}
	}
	t.Errorf("rolegroup %q should not exist in DB but was still found after 30s", rolegroupName)
}

func (c *RolegroupTestConfig) verifyRoleInRolegroupInDB(ctx context.Context, t *testing.T, roleName, expectedRolegroup string) {
	var rgName *string
	row := c.conn.QueryRowContext(ctx, "SELECT ROLEGROUP_NAME FROM SYS.ROLES WHERE ROLE_NAME = ?", roleName)
	if err := row.Scan(&rgName); err != nil {
		t.Fatalf("failed to query role %q: %v", roleName, err)
	}
	actual := ""
	if rgName != nil {
		actual = *rgName
	}
	if actual != expectedRolegroup {
		t.Errorf("expected role %q in rolegroup %q, got %q", roleName, expectedRolegroup, actual)
	} else {
		t.Logf("Verified role %q has rolegroup %q in HANA DB", roleName, expectedRolegroup)
	}
}

func (c *RolegroupTestConfig) createRolegroupCR(ctx context.Context, t *testing.T, cfg *envconf.Config, name string, spec v1alpha1.RolegroupParameters) *v1alpha1.Rolegroup {
	rg := &v1alpha1.Rolegroup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admin.hana.sap.crossplane.io/v1alpha1",
			Kind:       "Rolegroup",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.RolegroupSpec{
			ResourceSpec: xpv1.ResourceSpec{
				DeletionPolicy:          xpv1.DeletionDelete,
				ProviderConfigReference: &xpv1.Reference{Name: "example"},
			},
			ForProvider: spec,
		},
	}
	if err := c.Resource.Create(ctx, rg); err != nil {
		t.Fatalf("failed to create Rolegroup CR %q: %v", name, err)
	}
	c.RolegroupNames = append(c.RolegroupNames, spec.RolegroupName)
	return rg
}

func (c *RolegroupTestConfig) createRoleCR(ctx context.Context, t *testing.T, cfg *envconf.Config, name string, spec v1alpha1.RoleParameters) *v1alpha1.Role {
	role := &v1alpha1.Role{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admin.hana.sap.crossplane.io/v1alpha1",
			Kind:       "Role",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.RoleSpec{
			ResourceSpec: xpv1.ResourceSpec{
				DeletionPolicy:          xpv1.DeletionDelete,
				ProviderConfigReference: &xpv1.Reference{Name: "example"},
			},
			ForProvider: spec,
		},
	}
	if err := c.Resource.Create(ctx, role); err != nil {
		t.Fatalf("failed to create Role CR %q: %v", name, err)
	}
	c.RoleNames = append(c.RoleNames, spec.RoleName)
	return role
}

func (c *RolegroupTestConfig) waitForReady(ctx context.Context, t *testing.T, cfg *envconf.Config, obj k8s.Object) {
	res := cfg.Client().Resources()
	xpc := xpconditions.New(res)
	if err := wait.For(
		conditions.New(res).ResourceMatch(obj, xpc.IsManagedResourceReadyAndReady),
		wait.WithTimeout(5*time.Minute),
	); err != nil {
		t.Fatalf("%s %q did not become ready: %v", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
	}
}

func (c *RolegroupTestConfig) deleteCR(ctx context.Context, t *testing.T, cfg *envconf.Config, obj k8s.Object) {
	res := cfg.Client().Resources()
	if err := res.Delete(ctx, obj); err != nil {
		t.Fatalf("failed to delete %q: %v", obj.GetName(), err)
	}
	if err := wait.For(conditions.New(res).ResourceDeleted(obj), wait.WithTimeout(5*time.Minute)); err != nil {
		t.Errorf("%q was not deleted in time: %v", obj.GetName(), err)
	}
}

func describeRolegroup(t *testing.T, name string) {
	out, err := exec.Command("kubectl", "describe", "rolegroup", name).CombinedOutput()
	if err != nil {
		t.Logf("failed to run kubectl describe: %v", err)
	} else {
		t.Logf("kubectl describe rolegroup %s output:\n%s", name, string(out))
	}
}

// TestRolegroupLifecycle covers the happy path: create, verify in DB, update, verify update, delete, verify gone.
func TestRolegroupLifecycle(t *testing.T) {
	testConfig := resources.NewResourceTestConfig(nil, "Rolegroup")
	logger := logging.NewNopLogger()

	c := &RolegroupTestConfig{
		TestConfig: testConfig,
		db:         hana.New(logger),
	}

	fB := features.New("RolegroupLifecycle")
	fB.WithLabel("kind", testConfig.Kind)
	fB.Setup(c.SetupRolegroup)

	fB.Assess("create", testConfig.AssessCreate)

	fB.Assess("verify-in-db", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		c.verifyRolegroupExistsInDB(ctx, t, "E2ETESTROLEGROUPBASIC")
		return ctx
	})

	fB.Assess("update-disable-role-admin", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		rg := &v1alpha1.Rolegroup{}
		if err := c.Resource.Get(ctx, "e2e-test-rolegroup", "", rg); err != nil {
			t.Fatalf("failed to get rolegroup CR: %v", err)
		}
		rg.Spec.ForProvider.DisableRoleAdmin = true
		res := cfg.Client().Resources()
		if err := res.Update(ctx, rg); err != nil {
			t.Fatalf("failed to update rolegroup: %v", err)
		}

		err := wait.For(
			conditions.New(res).ResourceMatch(rg, func(obj k8s.Object) bool {
				r, ok := obj.(*v1alpha1.Rolegroup)
				if !ok {
					return false
				}
				return r.Status.AtProvider.DisableRoleAdmin
			}),
			wait.WithTimeout(5*time.Minute),
		)
		if err != nil {
			t.Errorf("rolegroup did not reflect update: %v", err)
			describeRolegroup(t, "e2e-test-rolegroup")
		}
		return ctx
	})

	fB.Assess("verify-update-in-db", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		var isEnabled string
		row := c.conn.QueryRowContext(ctx, "SELECT IS_ROLE_ADMIN_ENABLED FROM SYS.ROLEGROUPS WHERE ROLEGROUP_NAME = ?", "E2ETESTROLEGROUPBASIC")
		if err := row.Scan(&isEnabled); err != nil {
			t.Errorf("failed to query IS_ROLE_ADMIN_ENABLED: %v", err)
			return ctx
		}
		if isEnabled != "FALSE" {
			t.Errorf("expected IS_ROLE_ADMIN_ENABLED=FALSE, got %q", isEnabled)
		}
		return ctx
	})

	fB.Assess("delete", testConfig.AssessDelete)

	fB.Assess("verify-deleted-in-db", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		c.verifyRolegroupNotExistsInDB(ctx, t, "E2ETESTROLEGROUPBASIC")
		return ctx
	})

	fB.Teardown(c.TeardownRolegroup)
	testenv.Test(t, fB.Feature())
}

// TestRolegroupWithRoleAssignment verifies roles can be created in a rolegroup and deleted.
func TestRolegroupWithRoleAssignment(t *testing.T) {
	logger := logging.NewNopLogger()
	rolegroupName := "E2ETESTRGROLEASSIGN"
	roleName := "E2ETESTROLEWITHRG"
	rgCRName := "e2e-rg-for-role"
	roleCRName := "e2e-role-with-rolegroup"

	c := &RolegroupTestConfig{
		db: hana.New(logger),
	}

	fB := features.New("RolegroupWithRoleAssignment")
	fB.WithLabel("kind", "Rolegroup")

	fB.Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		c.connectDB(ctx, t)
		var err error
		if c.Resource, err = k8sresources.New(cfg.Client().RESTConfig()); err != nil {
			t.Fatalf("failed to create resource client: %v", err)
		}
		return ctx
	})

	fB.Assess("create-rolegroup", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		rg := c.createRolegroupCR(ctx, t, cfg, rgCRName, v1alpha1.RolegroupParameters{
			RolegroupName: rolegroupName,
		})
		c.waitForReady(ctx, t, cfg, rg)
		return ctx
	})

	fB.Assess("create-role-in-rolegroup", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		role := c.createRoleCR(ctx, t, cfg, roleCRName, v1alpha1.RoleParameters{
			RoleName:  roleName,
			Rolegroup: rolegroupName,
		})
		c.waitForReady(ctx, t, cfg, role)
		return ctx
	})

	fB.Assess("verify-role-in-rolegroup-in-db", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		c.verifyRoleInRolegroupInDB(ctx, t, roleName, rolegroupName)
		return ctx
	})

	fB.Assess("delete-role", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		role := &v1alpha1.Role{}
		if err := c.Resource.Get(ctx, roleCRName, "", role); err != nil {
			t.Fatalf("failed to get role: %v", err)
		}
		c.deleteCR(ctx, t, cfg, role)
		return ctx
	})

	fB.Assess("delete-rolegroup", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		rg := &v1alpha1.Rolegroup{}
		if err := c.Resource.Get(ctx, rgCRName, "", rg); err != nil {
			t.Fatalf("failed to get rolegroup: %v", err)
		}
		c.deleteCR(ctx, t, cfg, rg)
		return ctx
	})

	fB.Teardown(c.TeardownRolegroup)
	testenv.Test(t, fB.Feature())
}
