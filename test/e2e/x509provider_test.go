//go:build e2e

package e2e

// TestX509ProviderFlow drives the full X.509 identity chain (X509Provider →
// User with x509 identity) through the controllers against a live HANA Cloud
// instance. It verifies:
//
//   - IS_X509_ENABLED = TRUE is set in SYS.USERS after reconciliation (the
//     primary bug fixed in this change set).
//   - The providerRef in the User spec is resolved to the HANA-side provider
//     name so drift detection works; a separate assessNoDrift step confirms
//     ASSIGN_TIME in SYS.X509_USER_MAPPINGS does not change across reconcile
//     cycles (the secondary drift bug fixed in this change set).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/crossplane-contrib/xp-testing/pkg/resources"
	"github.com/crossplane-contrib/xp-testing/pkg/xpconditions"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	k8sresources "sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

type X509FlowTestConfig struct {
	TestConfig *resources.ResourceTestConfig
	Resource   *k8sresources.Resources
	Objects    []k8s.Object

	// HANA names tracked for SQL teardown fall-back.
	X509ProviderName string
	UserName         string

	db   xsql.Connector
	conn xsql.DB
}

func (c *X509FlowTestConfig) connectDB(ctx context.Context, t *testing.T) {
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

func (c *X509FlowTestConfig) SetupX509Flow(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Log("Prepare X.509 identity chain")
	c.connectDB(ctx, t)

	fixturePath := filepath.Join("testdata", "x509_flow.yaml")
	objects := make([]k8s.Object, 0)
	f, err := os.Open(fixturePath)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close() //nolint:errcheck
	if err := decoder.DecodeEach(
		ctx, f,
		func(ctx context.Context, obj k8s.Object) error {
			objects = append(objects, obj)
			return nil
		},
		decoder.MutateNamespace(cfg.Namespace()),
	); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	if c.Resource, err = k8sresources.New(cfg.Client().RESTConfig()); err != nil {
		t.Fatalf("create resource client: %v", err)
	}
	c.Objects = objects

	for _, obj := range objects {
		switch v := obj.(type) {
		case *v1alpha1.X509Provider:
			c.X509ProviderName = v.Spec.ForProvider.Name
		case *v1alpha1.User:
			c.UserName = v.Spec.ForProvider.Username
		}
	}
	c.cleanupX509FlowResources(ctx, t)
	return ctx
}

func (c *X509FlowTestConfig) TeardownX509Flow(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Log("Teardown: SQL fall-back for X.509 chain")
	c.cleanupX509FlowResources(ctx, t)
	if err := c.db.Disconnect(); err != nil {
		t.Errorf("failed to disconnect: %v", err)
	}
	return ctx
}

func (c *X509FlowTestConfig) cleanupX509FlowResources(ctx context.Context, t *testing.T) {
	stmts := []string{}
	if c.UserName != "" {
		stmts = append(stmts, fmt.Sprintf("DROP USER %s CASCADE", c.UserName))
	}
	if c.X509ProviderName != "" {
		stmts = append(stmts, fmt.Sprintf("DROP X509 PROVIDER %s CASCADE", c.X509ProviderName))
	}
	for _, s := range stmts {
		if _, err := c.conn.ExecContext(ctx, s); err != nil {
			t.Logf("cleanup (ok if absent): %s -- %v", s, err)
		}
	}
}

// TestX509ProviderFlow is the end-to-end coverage for the X.509 identity chain.
func TestX509ProviderFlow(t *testing.T) {
	testConfig := resources.NewResourceTestConfig(nil, "X509Provider")
	logger := logging.NewNopLogger()

	c := &X509FlowTestConfig{
		TestConfig: testConfig,
		db:         hana.New(logger),
	}

	fB := features.New("X509ProviderFlow")
	fB.WithLabel("kind", "X509Provider")
	fB.Setup(c.SetupX509Flow)

	// Create X509Provider before User: the User reconciler resolves the
	// providerRef at reconcile time, so the X509Provider must exist in HANA
	// first.
	fB.Assess("create-chain", c.assessOrderedCreate)
	fB.Assess("observation-shape", c.assessObservationShape)
	fB.Assess("no-drift", c.assessNoDrift)
	fB.Assess("delete-chain", c.assessOrderedDelete)

	fB.Teardown(c.TeardownX509Flow)

	testenv.Test(t, fB.Feature())
}

func (c *X509FlowTestConfig) assessOrderedCreate(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	res := cfg.Client().Resources()
	for _, obj := range c.orderedCreateObjects(t) {
		if err := res.Create(ctx, obj); err != nil {
			t.Fatalf("failed to create %s %q: %v", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
		}
		c.waitForReady(ctx, t, cfg, obj)
	}
	return ctx
}

func (c *X509FlowTestConfig) orderedCreateObjects(t *testing.T) []k8s.Object {
	var x509Provider, user k8s.Object
	for _, obj := range c.Objects {
		switch obj.(type) {
		case *v1alpha1.X509Provider:
			x509Provider = obj
		case *v1alpha1.User:
			user = obj
		}
	}
	for _, obj := range []k8s.Object{x509Provider, user} {
		if obj == nil {
			t.Fatalf("X509 flow fixture did not decode all expected resources")
		}
	}
	return []k8s.Object{x509Provider, user}
}

func (c *X509FlowTestConfig) waitForReady(ctx context.Context, t *testing.T, cfg *envconf.Config, obj k8s.Object) {
	res := cfg.Client().Resources()
	xpc := xpconditions.New(res)
	if err := wait.For(
		conditions.New(res).ResourceMatch(obj, xpc.IsManagedResourceReadyAndReady),
		wait.WithTimeout(5*time.Minute),
	); err != nil {
		out, _ := exec.Command("kubectl", "describe",
			obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName()).CombinedOutput()
		t.Fatalf("%s %q did not become ready: %v\nkubectl describe:\n%s",
			obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err, string(out))
	}
}

// assessObservationShape verifies that after creation:
//   - User.Status.AtProvider.IsX509Enabled is true (ENABLE X509 was executed)
//   - User.Status.AtProvider.X509Providers contains the expected mapping with
//     the resolved HANA provider name (not the Kubernetes resource name)
func (c *X509FlowTestConfig) assessObservationShape(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	for _, obj := range c.Objects {
		if err := c.Resource.Get(ctx, obj.GetName(), obj.GetNamespace(), obj); err != nil {
			t.Errorf("failed to re-fetch %s/%s: %v", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
			continue
		}
		switch v := obj.(type) {
		case *v1alpha1.X509Provider:
			at := v.Status.AtProvider
			if at.Name == nil || *at.Name != c.X509ProviderName {
				t.Errorf("X509Provider.Status.AtProvider.Name: want %q, got %v", c.X509ProviderName, at.Name)
			}
		case *v1alpha1.User:
			at := v.Status.AtProvider

			if at.IsX509Enabled == nil || !*at.IsX509Enabled {
				t.Errorf("User.Status.AtProvider.IsX509Enabled: want true, got %v — ENABLE X509 was not executed", at.IsX509Enabled)
			}
			if at.IsClientConnectEnabled == nil || !*at.IsClientConnectEnabled {
				t.Errorf("User.Status.AtProvider.IsClientConnectEnabled: want true, got %v", at.IsClientConnectEnabled)
			}

			if len(at.X509Providers) != 1 {
				t.Errorf("User.Status.AtProvider.X509Providers: want 1 entry, got %d: %+v", len(at.X509Providers), at.X509Providers)
				break
			}
			mapping := at.X509Providers[0]
			// The observed Name comes from SYS.X509_USER_MAPPINGS and must be
			// the HANA provider name, not the Kubernetes resource name.
			if mapping.Name != c.X509ProviderName {
				t.Errorf("User.Status.AtProvider.X509Providers[0].Name: want HANA name %q, got %q — providerRef was not resolved", c.X509ProviderName, mapping.Name)
			}
			if mapping.SubjectName != "ANY" {
				t.Errorf("User.Status.AtProvider.X509Providers[0].SubjectName: want %q, got %q", "ANY", mapping.SubjectName)
			}
		}
	}
	return ctx
}

// assessNoDrift verifies that the identity mapping ASSIGN_TIME in
// SYS.X509_USER_MAPPINGS does not change across reconcile cycles. Before the
// drift fix, the controller continuously re-issued DROP IDENTITY + ADD IDENTITY
// because isX509MappingsUpToDate compared the unresolved providerRef name
// against the resolved HANA name, so they never matched.
func (c *X509FlowTestConfig) assessNoDrift(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	// Record the ASSIGN_TIME after the initial reconcile has settled.
	var assignTimeBefore string
	row := c.conn.QueryRowContext(ctx,
		"SELECT TO_VARCHAR(ASSIGN_TIME, 'YYYY-MM-DD HH24:MI:SS') FROM SYS.X509_USER_MAPPINGS WHERE USER_NAME = ?",
		c.UserName,
	)
	if err := row.Scan(&assignTimeBefore); err != nil {
		t.Fatalf("failed to read ASSIGN_TIME before drift window: %v", err)
	}
	t.Logf("X509 ASSIGN_TIME before drift window: %s", assignTimeBefore)

	// Wait two poll intervals so at least one extra reconcile fires.
	time.Sleep(30 * time.Second)

	var assignTimeAfter string
	row = c.conn.QueryRowContext(ctx,
		"SELECT TO_VARCHAR(ASSIGN_TIME, 'YYYY-MM-DD HH24:MI:SS') FROM SYS.X509_USER_MAPPINGS WHERE USER_NAME = ?",
		c.UserName,
	)
	if err := row.Scan(&assignTimeAfter); err != nil {
		t.Fatalf("failed to read ASSIGN_TIME after drift window: %v", err)
	}
	t.Logf("X509 ASSIGN_TIME after drift window:  %s", assignTimeAfter)

	if assignTimeBefore != assignTimeAfter {
		t.Errorf("ASSIGN_TIME changed (%s → %s): controller is continuously re-applying the X.509 identity mapping (drift bug)", assignTimeBefore, assignTimeAfter)
	}
	return ctx
}

// assessOrderedDelete deletes User before X509Provider. Deleting X509Provider
// first while a user still holds an ADD IDENTITY mapping causes HANA to reject
// the DROP X509 PROVIDER with a dependency error.
func (c *X509FlowTestConfig) assessOrderedDelete(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	res := cfg.Client().Resources()

	ordered := []struct {
		name string
		obj  k8s.Object
	}{
		{"e2e-x509-user", &v1alpha1.User{}},
		{"e2e-x509-provider", &v1alpha1.X509Provider{}},
	}
	for _, k := range ordered {
		if err := res.Get(ctx, k.name, cfg.Namespace(), k.obj); err != nil {
			t.Errorf("failed to get %s before delete: %v", k.name, err)
			continue
		}
		if err := res.Delete(ctx, k.obj); err != nil {
			t.Errorf("failed to delete %s: %v", k.name, err)
			continue
		}
		if err := wait.For(
			conditions.New(res).ResourceDeleted(k.obj),
			wait.WithTimeout(3*time.Minute),
		); err != nil {
			out, _ := exec.Command("kubectl", "describe",
				k.obj.GetObjectKind().GroupVersionKind().Kind, k.name).CombinedOutput()
			t.Errorf("%s not garbage-collected: %v\nkubectl describe:\n%s", k.name, err, string(out))
		}
	}
	return ctx
}
