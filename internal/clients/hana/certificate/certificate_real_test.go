package certificate

import (
	"context"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/logging"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
)

// TestCertificate_RealConnection exercises Read, Create, and Delete against a
// live HANA instance. It is skipped in normal CI — remove the t.Skip call and
// fill in real credentials to run it manually.
func TestCertificate_RealConnection(t *testing.T) {
	t.Skipf("for debugging only, requires real connection to HANA DB")

	creds := map[string][]byte{
		"endpoint": []byte("hostonly.example.com"),
		"port":     []byte("443"),
		"username": []byte("MYUSER"),
		"password": []byte("Hana)/CompliantPassword123!"),
	}

	// A minimal self-signed PEM certificate for testing. Replace with a real
	// PEM if you need the import to succeed for subsequent PSE/X509 steps.
	testPEM := []byte(`-----BEGIN CERTIFICATE-----
MIICpDCCAYwCCQDU+pQ4pHgSpDANBgkqhkiG9w0BAQsFADAUMRIwEAYDVQQDDAls
b2NhbGhvc3QwHhcNMjUwMTAxMDAwMDAwWhcNMjYwMTAxMDAwMDAwWjAUMRIwEAYD
VQQDDAlsb2NhbGhvc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQC7
o4qne60TB3wolKELsqocy6YfA22Kc6JcjuHFr4dpMjzFLCEMAAAAAAAHAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAMBAAEw
DQYJKoZIhvcNAQELBQADggEBABKhF+sTMcmJBgHkFbIBVbFDiQEAAAAAAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
-----END CERTIFICATE-----
`)

	ctx := context.Background()

	db := hana.New(logging.NewNopLogger())
	conn, err := db.Connect(ctx, creds)
	if err != nil {
		t.Fatalf("failed to connect to HANA DB: %v", err)
	}

	client := New(conn)
	params := &v1alpha1.CertificateParameters{
		Name: "e2e-test-cert",
	}

	// --- Read (before create — should return nil) ---
	obs, err := client.Read(ctx, params)
	if err != nil {
		t.Fatalf("Read before create failed: %v", err)
	}
	if obs != nil {
		t.Logf("certificate already exists before create: %+v", obs)
	} else {
		t.Log("Read before create: no certificates found (expected)")
	}

	// --- Create ---
	if err := client.Create(ctx, params, testPEM); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	t.Log("Create succeeded")

	// --- Read (after create — should return at least one entry) ---
	obs, err = client.Read(ctx, params)
	if err != nil {
		t.Fatalf("Read after create failed: %v", err)
	}
	if obs == nil || len(obs.Certificates) == 0 {
		t.Fatal("Read after create returned no certificates")
	}
	t.Logf("Read after create: %+v", obs.Certificates)

	// --- Delete ---
	if err := client.Delete(ctx, params); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	t.Log("Delete succeeded")

	// --- Read (after delete — should return nil) ---
	obs, err = client.Read(ctx, params)
	if err != nil {
		t.Fatalf("Read after delete failed: %v", err)
	}
	if obs != nil {
		t.Errorf("expected no certificates after delete, got: %+v", obs.Certificates)
	} else {
		t.Log("Read after delete: no certificates found (expected)")
	}
}
