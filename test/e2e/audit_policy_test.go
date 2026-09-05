//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/crossplane-contrib/xp-testing/pkg/resources"

	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	k8sresources "sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

type AuditPolicyTestConfig struct {
	TestConfig *resources.ResourceTestConfig
	Resource   *k8sresources.Resources
	Objects    []k8s.Object
}

func TestAuditPolicy(t *testing.T) {
	testConfig := resources.NewResourceTestConfig(nil, "AuditPolicy")

	c := &AuditPolicyTestConfig{
		TestConfig: testConfig,
	}

	fB := features.New(fmt.Sprintf("%v", testConfig.Kind))
	fB.WithLabel("kind", testConfig.Kind)
	fB.Setup(c.SetupAuditPolicy)

	fB.Assess("create", testConfig.AssessCreate)

	fB.Assess("update", c.assessUpdate)

	fB.Assess("delete", testConfig.AssessDelete)

	fB.Teardown(testConfig.Teardown)

	testenv.Test(t, fB.Feature())
}

func (c *AuditPolicyTestConfig) assessUpdate(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	for _, obj := range c.Objects {
		if err := c.Resource.Get(ctx, obj.GetName(), obj.GetNamespace(), obj); err != nil {
			t.Errorf("failed to get audit policy: %v", err)
			return ctx
		}

		auditPolicy, ok := obj.(*v1alpha1.AuditPolicy)
		if !ok {
			t.Errorf("failed to cast object to AuditPolicy: %v", obj)
			return ctx
		}

		auditPolicy.Spec.ForProvider.AuditActions = append(auditPolicy.Spec.ForProvider.AuditActions,
			"REVOKE ANY",
		)
		res := cfg.Client().Resources()

		err := res.Update(ctx, auditPolicy)
		if err != nil {
			t.Fatal(err)
		}

		var fn = func(u k8s.Object) bool {
			return u.GetName() == auditPolicy.GetName() && u.GetNamespace() == auditPolicy.GetNamespace()
		}

		var areAuditActionsUpToDate = func(observed, desired []string) bool {
			if len(observed) != len(desired) {
				return false
			}
			for i, action := range observed {
				if action != desired[i] {
					return false
				}
			}
			return true
		}

		err = wait.For(
			conditions.New(res).ResourceMatch(auditPolicy, fn),
		)
		if err != nil {
			t.Error(err)
		}
		if err := res.Get(ctx, auditPolicy.GetName(), auditPolicy.GetNamespace(), auditPolicy); err != nil {
			t.Errorf("failed to get audit policy after update: %v", err)
		} else if !areAuditActionsUpToDate(auditPolicy.Spec.ForProvider.AuditActions, auditPolicy.Spec.ForProvider.AuditActions) {
			t.Errorf("audit policy update failed, expected audit actions to be [GRANT ANY, REVOKE ANY], got %v", auditPolicy.Spec.ForProvider.AuditActions)
		}
	}

	return ctx
}

// Setup creates the resource in the cluster
func (c *AuditPolicyTestConfig) SetupAuditPolicy(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Logf("Apply AuditPolicy")

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
		if _, ok := obj.(*v1alpha1.AuditPolicy); !ok {
			t.Errorf("failed to cast object to AuditPolicy: %v", obj)
			return ctx
		}

		c.Objects = append(c.Objects, obj)
	}

	return ctx
}
