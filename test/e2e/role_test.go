//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/crossplane-contrib/xp-testing/pkg/resources"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	k8sresources "sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

type RoleTestConfig struct {
	TestConfig *resources.ResourceTestConfig
	Resource   *k8sresources.Resources
	Objects    []k8s.Object
}

func TestRole(t *testing.T) {
	testConfig := resources.NewResourceTestConfig(nil, "Role")

	c := &RoleTestConfig{TestConfig: testConfig}

	fB := features.New(fmt.Sprintf("%v", testConfig.Kind))
	fB.WithLabel("kind", testConfig.Kind)
	fB.Setup(c.SetupRole)

	fB.Assess("create", testConfig.AssessCreate)

	fB.Assess("update", c.assessUpdate)

	fB.Assess("delete", testConfig.AssessDelete)

	fB.Teardown(testConfig.Teardown)

	testenv.Test(t, fB.Feature())
}

func (c *RoleTestConfig) assessUpdate(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	for _, obj := range c.Objects {
		if err := c.Resource.Get(ctx, obj.GetName(), obj.GetNamespace(), obj); err != nil {
			t.Errorf("failed to get role: %v", err)
			return ctx
		}

		role, ok := obj.(*v1alpha1.Role)
		if !ok {
			t.Errorf("failed to cast object to Role: %v", obj)
			return ctx
		}

		role.Spec.ForProvider.Privileges = append(role.Spec.ForProvider.Privileges,
			"AUDIT READ",
		)
		res := cfg.Client().Resources()

		err := res.Update(ctx, role)
		if err != nil {
			t.Fatal(err)
		}

		var fn = func(u k8s.Object) bool {
			return u.GetName() == role.GetName() && u.GetNamespace() == role.GetNamespace()
		}

		err = wait.For(
			conditions.New(res).ResourceMatch(role, fn),
		)
		if err != nil {
			t.Error(err)
		}

		less := func(a, b string) bool { return a < b }
		equalIgnoreOrder := cmp.Diff(role.Spec.ForProvider.Privileges, role.Status.AtProvider.Privileges, cmpopts.SortSlices(less)) == ""

		if !equalIgnoreOrder {
			t.Errorf("failed to update role privileges: Status does not match Spec. Name: %s, Namespace: %s", role.Name, role.Namespace)

			out, derr := exec.Command("kubectl", "describe", "role", role.Name, "-n", role.Namespace).CombinedOutput()
			if derr != nil {
				t.Errorf("failed to run kubectl describe: %v", derr)
			} else {
				t.Logf("kubectl describe role output:\n%s", string(out))
			}
		}
	}

	return ctx
}

// Setup creates the resource and secret in the cluster
func (c *RoleTestConfig) SetupRole(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Logf("Apply Role")

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

	for _, obj := range objects {
		if _, ok := obj.(*v1alpha1.Role); !ok {
			t.Errorf("failed to cast object to Role: %v", obj)
			return ctx
		}

		c.Objects = append(c.Objects, obj)
	}

	return ctx
}
