//go:build e2e

package e2e

// TestJWTProviderFlow drives the full JWT-SSO chain (PublicKey → PSE →
// JWTProvider → User with JWT identity) through the controllers against a live
// HANA Cloud instance. Replaces the standalone integration harness at
// test/integration/jwt_flow_test.go: the same "SQL is accepted by HANA and
// SYS views parse back into observations" signal, but through the controller
// stack the same way every other resource proves it. See PR 125 for context.
//
// The public key is generated at Setup time so the fixture stays checked in
// while every run gets a fresh key; HANA verifies key shape at import time,
// not signature against any token.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/crossplane-contrib/xp-testing/pkg/resources"
	"github.com/crossplane/crossplane-runtime/pkg/logging"

	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	k8sresources "sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

type JWTFlowTestConfig struct {
	TestConfig *resources.ResourceTestConfig
	Resource   *k8sresources.Resources
	Objects    []k8s.Object

	// HANA names of the four resources, tracked for teardown fall-back.
	PublicKeyName   string
	JWTProviderName string
	PSEName         string
	UserName        string

	db   xsql.Connector
	conn xsql.DB
}

func (c *JWTFlowTestConfig) connectDB(ctx context.Context, t *testing.T) {
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

// renderFixture generates a fresh RSA public key, renders the template with
// it into a per-run tempdir, and returns that directory. AssessCreate reads
// files from there; the template lives in test/e2e/testdata/ (outside `crs/`)
// so ImportResources does not pick up the raw template as YAML.
func renderFixture(t *testing.T) string {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("x509.MarshalPKIXPublicKey: %v", err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	// YAML block-scalar indent: each line of the PEM must be prefixed by the
	// six spaces that align with `pem: |` in the template.
	var indented strings.Builder
	for _, line := range strings.Split(strings.TrimRight(pemStr, "\n"), "\n") {
		indented.WriteString("      ")
		indented.WriteString(line)
		indented.WriteString("\n")
	}

	tmplPath := filepath.Join("testdata", "jwt_flow.yaml.tmpl")
	raw, err := os.ReadFile(tmplPath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	tmpl, err := template.New("fixture").Parse(string(raw))
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, map[string]string{"PEM": indented.String()}); err != nil {
		t.Fatalf("execute template: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "jwt_flow.yaml"), out.Bytes(), 0o600); err != nil {
		t.Fatalf("write rendered fixture: %v", err)
	}
	return dir
}

func (c *JWTFlowTestConfig) SetupJWTFlow(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Log("Apply JWT-SSO chain")
	c.connectDB(ctx, t)

	// Redirect the ResourceTestConfig at our per-run tempdir with the
	// rendered fixture, so AssessCreate/AssessDelete pick up the same file.
	c.TestConfig.ResourceDirectory = renderFixture(t)

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
	c.Objects = objects

	// Track HANA names for teardown fall-back, using the actual types we know
	// are in the fixture.
	for _, obj := range objects {
		switch v := obj.(type) {
		case *v1alpha1.PublicKey:
			c.PublicKeyName = v.Spec.ForProvider.Name
		case *v1alpha1.JWTProvider:
			c.JWTProviderName = v.Spec.ForProvider.Name
		case *v1alpha1.PersonalSecurityEnvironment:
			c.PSEName = v.Spec.ForProvider.Name
		case *v1alpha1.User:
			c.UserName = v.Spec.ForProvider.Username
		}
	}
	return ctx
}

func (c *JWTFlowTestConfig) TeardownJWTFlow(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	t.Log("Teardown: fall-back JWT-SSO chain cleanup via SQL")
	// AssessDelete already deletes via the controllers. This is a safety net
	// in case reconcile got stuck: same dependency order the integration test
	// used, best-effort ignore-errors.
	stmts := []string{}
	if c.UserName != "" {
		stmts = append(stmts, fmt.Sprintf("DROP USER %s CASCADE", c.UserName))
	}
	if c.PSEName != "" {
		stmts = append(stmts, fmt.Sprintf("DROP PSE %s", c.PSEName))
	}
	if c.JWTProviderName != "" {
		stmts = append(stmts, fmt.Sprintf("DROP JWT PROVIDER %s", c.JWTProviderName))
	}
	if c.PublicKeyName != "" {
		stmts = append(stmts, fmt.Sprintf("DROP PUBLIC KEY %s", c.PublicKeyName))
	}
	for _, s := range stmts {
		if _, err := c.conn.ExecContext(ctx, s); err != nil {
			t.Logf("cleanup (ok if absent): %s -- %v", s, err)
		}
	}
	if err := c.db.Disconnect(); err != nil {
		t.Errorf("failed to disconnect from database: %v", err)
	}
	return ctx
}

// TestJWTProviderFlow is the single e2e replacement for the deleted
// integration test. It applies the four-CR chain, waits for Ready, verifies
// observations round-trip through SYS views, then drives two drift scenarios:
// a claim-filter value change on the provider, and a CLIENT CONNECT toggle on
// the user.
func TestJWTProviderFlow(t *testing.T) {
	testConfig := resources.NewResourceTestConfig(nil, "JWTProvider")
	logger := logging.NewNopLogger()

	c := &JWTFlowTestConfig{
		TestConfig: testConfig,
		db:         hana.New(logger),
	}

	fB := features.New("JWTProviderFlow")
	fB.WithLabel("kind", "JWTProvider")
	fB.Setup(c.SetupJWTFlow)

	// AssessCreate waits for every CR in ResourceDirectory to reach Ready,
	// which for this chain means the full DDL sequence (CREATE PUBLIC KEY →
	// CREATE PSE PURPOSE JWT → CREATE JWT PROVIDER → SET PSE ... FOR PROVIDER
	// → CREATE RESTRICTED USER + ENABLE JWT + ADD IDENTITY + ENABLE CLIENT
	// CONNECT) succeeded against real HANA.
	fB.Assess("create-chain", testConfig.AssessCreate)

	fB.Assess("observation-shape", c.assessObservationShape)
	fB.Assess("drift-claim-filter", c.assessClaimFilterDrift)
	// Note: no drift-client-connect assess. `EnableClientConnect bool` on the
	// User spec has `json:"...,omitempty"` and a `+kubebuilder:default:=true`
	// CRD default, so a spec toggle to false is serialized as "field absent"
	// and admission re-applies the default. The Create-time DDL is still
	// covered by observation-shape (IsClientConnectEnabled == true), and the
	// ToggleClientConnect SQL emission is covered in user_client_test.go.

	// Ordered delete: the User references the JWTProvider via ADD IDENTITY,
	// and the PSE references both the JWTProvider (SET PSE ... FOR PROVIDER)
	// and the PublicKey (ADD PUBLIC KEY). testConfig.AssessDelete deletes all
	// CRs concurrently, which races reconciliation and can hang User's
	// finalizer removal for the full 5-minute timeout. Explicitly delete in
	// dependency order (User → PSE → JWTProvider → PublicKey) instead.
	fB.Assess("delete-chain", c.assessOrderedDelete)

	fB.Teardown(c.TeardownJWTFlow)

	testenv.Test(t, fB.Feature())
}

// assessObservationShape re-fetches every CR after AssessCreate and asserts
// that the SYS view round-trip produced sane observations. Runs cheap
// sanity-only checks; drift-specific assertions live in their own assess-
// funcs so failures are attributed accurately.
func (c *JWTFlowTestConfig) assessObservationShape(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	for _, obj := range c.Objects {
		if err := c.Resource.Get(ctx, obj.GetName(), obj.GetNamespace(), obj); err != nil {
			t.Errorf("failed to re-fetch %s/%s: %v", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
			continue
		}
		switch v := obj.(type) {
		case *v1alpha1.PublicKey:
			at := v.Status.AtProvider
			if at.Algorithm == nil || !strings.HasPrefix(*at.Algorithm, "RSA") {
				t.Errorf("PublicKey.Status.AtProvider.Algorithm: want RSA*, got %v", at.Algorithm)
			}
		case *v1alpha1.JWTProvider:
			at := v.Status.AtProvider
			if at.Issuer == nil || *at.Issuer != "https://e2e.example.invalid" {
				t.Errorf("JWTProvider.Status.AtProvider.Issuer: got %v", at.Issuer)
			}
			if at.ApplicationUserClaim != "azp" {
				t.Errorf("JWTProvider.Status.AtProvider.ApplicationUserClaim: want azp, got %q", at.ApplicationUserClaim)
			}
			if len(at.ClaimFilters) != 1 || at.ClaimFilters[0].Claim != "groups" ||
				at.ClaimFilters[0].Value != "00000000-0000-0000-0000-deadbeefcafe" {
				t.Errorf("JWTProvider.Status.AtProvider.ClaimFilters: unexpected %+v", at.ClaimFilters)
			}
		case *v1alpha1.PersonalSecurityEnvironment:
			at := v.Status.AtProvider
			if at.JWTProviderName != c.JWTProviderName {
				t.Errorf("PSE.Status.AtProvider.JWTProviderName: want %q, got %q", c.JWTProviderName, at.JWTProviderName)
			}
			found := false
			for _, k := range at.PublicKeys {
				if k == c.PublicKeyName {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("PSE.Status.AtProvider.PublicKeys does not contain %q: %v", c.PublicKeyName, at.PublicKeys)
			}
		case *v1alpha1.User:
			at := v.Status.AtProvider
			if at.IsJWTEnabled == nil || !*at.IsJWTEnabled {
				t.Errorf("User.Status.AtProvider.IsJWTEnabled: want true, got %v", at.IsJWTEnabled)
			}
			if at.IsClientConnectEnabled == nil || !*at.IsClientConnectEnabled {
				t.Errorf("User.Status.AtProvider.IsClientConnectEnabled: want true, got %v", at.IsClientConnectEnabled)
			}
			if len(at.JWTProviders) != 1 ||
				at.JWTProviders[0].Name != c.JWTProviderName ||
				at.JWTProviders[0].ExternalIdentity != "e2e-user@example.com" {
				t.Errorf("User.Status.AtProvider.JWTProviders: unexpected %+v", at.JWTProviders)
			}
		}
	}
	return ctx
}

// assessClaimFilterDrift replaces the groups filter value and waits for the
// controller to reflect the new value in status. Covers the UNSET+SET DDL path
// the unit tests exercise; here we prove it round-trips through HANA.
func (c *JWTFlowTestConfig) assessClaimFilterDrift(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	jp := &v1alpha1.JWTProvider{}
	if err := c.Resource.Get(ctx, "e2e-jwt-provider", "", jp); err != nil {
		t.Fatalf("failed to get JWTProvider: %v", err)
	}
	newValue := "11111111-2222-3333-4444-555555555555"
	jp.Spec.ForProvider.ClaimFilters = []v1alpha1.JWTClaimFilter{
		{Claim: "groups", Value: newValue},
	}
	res := cfg.Client().Resources()
	if err := res.Update(ctx, jp); err != nil {
		t.Fatalf("failed to update JWTProvider claim filter: %v", err)
	}

	err := wait.For(
		conditions.New(res).ResourceMatch(jp, func(obj k8s.Object) bool {
			got, ok := obj.(*v1alpha1.JWTProvider)
			if !ok {
				return false
			}
			cf := got.Status.AtProvider.ClaimFilters
			return len(cf) == 1 && cf[0].Claim == "groups" && cf[0].Value == newValue
		}),
		wait.WithTimeout(5*time.Minute),
	)
	if err != nil {
		out, _ := exec.Command("kubectl", "describe", "jwtprovider", "e2e-jwt-provider").CombinedOutput()
		t.Errorf("claim filter drift did not propagate to observation: %v\nkubectl describe:\n%s", err, string(out))
	}
	return ctx
}

// assessOrderedDelete deletes the four CRs in dependency order, waiting for
// each to be garbage-collected before starting the next. Concurrent deletion
// via AssessDelete races the reconciler: DROP JWT PROVIDER fails on HANA
// while a live user has an ADD IDENTITY mapping, DROP PSE fails while its
// public keys or provider are being removed, and the User finalizer can hang
// for the full 5-minute timeout as a result. Explicit ordering keeps the
// controller's Delete path deterministic.
func (c *JWTFlowTestConfig) assessOrderedDelete(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	res := cfg.Client().Resources()

	// Kind order matches the reverse of Create's DDL dependency.
	kinds := []struct {
		name string
		obj  k8s.Object
	}{
		{"e2e-jwt-user", &v1alpha1.User{}},
		{"e2e-jwt-pse", &v1alpha1.PersonalSecurityEnvironment{}},
		{"e2e-jwt-provider", &v1alpha1.JWTProvider{}},
		{"e2e-jwt-key", &v1alpha1.PublicKey{}},
	}
	for _, k := range kinds {
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
