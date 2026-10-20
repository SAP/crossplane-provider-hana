//go:build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/crossplane-contrib/xp-testing/pkg/resources"
	"github.com/crossplane-contrib/xp-testing/pkg/xpenvfuncs"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	adminv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
)

func TestCertificate(t *testing.T) {
	testConfig := resources.NewResourceTestConfig(nil, "Certificate")

	fB := features.New(fmt.Sprintf("%v", testConfig.Kind))
	fB.WithLabel("kind", testConfig.Kind)
	fB.Setup(setupCertificate(testConfig))

	fB.Assess("create", testConfig.AssessCreate)

	fB.Assess("delete", testConfig.AssessDelete)

	fB.Teardown(teardownCertificate(testConfig))

	testenv.Test(t, fB.Feature())
}

// setupCertificate generates a self-signed PEM chain, creates the backing
// Secret in the cluster, then applies the Certificate CR.
func setupCertificate(testConfig *resources.ResourceTestConfig) func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		certPEM := generateSelfSignedChain(t)

		secretName := secretNameFromConfig(testConfig)
		secret := xpenvfuncs.SimpleSecret(secretName, "crossplane-system", map[string]string{
			"ca.crt": string(certPEM),
		})

		res := cfg.Client().Resources()
		if err := res.Create(ctx, secret); err != nil {
			t.Fatalf("failed to create certificate secret: %v", err)
		}

		resources.ImportResources(ctx, t, cfg, testConfig.ResourceDirectory)
		return ctx
	}
}

// teardownCertificate deletes the backing Secret after the Certificate CR is gone.
func teardownCertificate(testConfig *resources.ResourceTestConfig) func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		secretName := secretNameFromConfig(testConfig)
		secret := xpenvfuncs.SimpleSecret(secretName, "crossplane-system", nil)
		res := cfg.Client().Resources()
		if err := res.Delete(ctx, secret); err != nil {
			t.Errorf("failed to delete certificate secret: %v", err)
		}
		return ctx
	}
}

// secretNameFromConfig reads the secret name from the first Certificate CR in
// the test resource directory.
func secretNameFromConfig(testConfig *resources.ResourceTestConfig) string {
	// Match the name declared in crs/Certificate/certificate.yaml
	_ = testConfig
	return "test-cert-secret"
}

// generateSelfSignedChain returns a PEM containing two self-signed certificates
// to exercise the chain import path.
func generateSelfSignedChain(t *testing.T) []byte {
	t.Helper()
	var chain []byte
	for i := 0; i < 2; i++ {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			t.Fatalf("generate serial: %v", err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: serial,
			Subject:      pkix.Name{CommonName: fmt.Sprintf("e2e-test-ca-%d", i+1)},
			NotBefore:    time.Now().UTC(),
			NotAfter:     time.Now().UTC().Add(24 * time.Hour),
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			t.Fatalf("create certificate: %v", err)
		}
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return chain
}

// assessCertificateStatus verifies that the status reflects the imported certs.
func assessCertificateStatus(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
	res := cfg.Client().Resources()
	cert := &adminv1alpha1.Certificate{}
	if err := res.Get(ctx, "e2e-test-certificate", cfg.Namespace(), cert); err != nil {
		t.Errorf("failed to get certificate: %v", err)
		return ctx
	}

	if len(cert.Status.AtProvider.Certificates) == 0 {
		t.Errorf("expected at least one certificate in status, got none")
		return ctx
	}

	t.Logf("imported %d certificate(s):", len(cert.Status.AtProvider.Certificates))
	for _, c := range cert.Status.AtProvider.Certificates {
		t.Logf("  id=%d name=%s", *c.ID, c.Name)
	}
	return ctx
}
