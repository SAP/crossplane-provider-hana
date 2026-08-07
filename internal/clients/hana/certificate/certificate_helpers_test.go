package certificate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func pemCert(content string) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte(content)})
}

// selfSignedPEM generates a minimal self-signed certificate PEM for testing.
// serial and notBefore are fixed so derived names are deterministic.
func selfSignedPEM(t *testing.T, serial *big.Int, notBefore time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    notBefore,
		NotAfter:     notBefore.Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// nolint: contextcheck
func TestSplitPEMChain(t *testing.T) {
	cases := map[string]struct {
		reason    string
		input     []byte
		wantCerts [][]byte
		wantErr   string
	}{
		"EmptyInput": {
			reason:  "Empty input should error because no certificates are present",
			input:   []byte{},
			wantErr: "no certificates found in PEM chain",
		},
		"WhitespaceOnly": {
			reason:  "Whitespace-only input should error because no certificates are present",
			input:   []byte("   \n  \t  "),
			wantErr: "no certificates found in PEM chain",
		},
		"InvalidPEM": {
			reason:  "Non-PEM data should error with a decode failure message",
			input:   []byte("this is not valid pem data"),
			wantErr: "failed to decode PEM certificate",
		},
		"WrongBlockType": {
			reason: "A PEM block of type PRIVATE KEY should error with an unexpected block type message",
			input: pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: []byte("key-bytes"),
			}),
			wantErr: `unexpected PEM block type "PRIVATE KEY"`,
		},
		"MixedBlockTypes": {
			reason: "A chain whose second block is not a CERTIFICATE should error with an unexpected block type message",
			input: append(
				pemCert("cert-bytes"),
				pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("key-bytes")})...,
			),
			wantErr: `unexpected PEM block type "PRIVATE KEY"`,
		},
		"SingleCertificate": {
			reason:    "A single valid PEM certificate block should return that exact block",
			input:     pemCert("cert-one"),
			wantCerts: [][]byte{pemCert("cert-one")},
		},
		"TwoCertificateChain": {
			reason: "Two concatenated PEM blocks should return both blocks in original order",
			input:  append(pemCert("cert-one"), pemCert("cert-two")...),
			wantCerts: [][]byte{
				pemCert("cert-one"),
				pemCert("cert-two"),
			},
		},
		"ThreeCertificateChain": {
			reason: "Three concatenated PEM blocks should return all three blocks in original order",
			input:  append(append(pemCert("cert-one"), pemCert("cert-two")...), pemCert("cert-three")...),
			wantCerts: [][]byte{
				pemCert("cert-one"),
				pemCert("cert-two"),
				pemCert("cert-three"),
			},
		},
		"OrderIsPreserved": {
			reason: "Certificates must appear in the same order as they appear in the PEM input",
			input:  append(append(pemCert("root-ca"), pemCert("intermediate-ca")...), pemCert("leaf")...),
			wantCerts: [][]byte{
				pemCert("root-ca"),
				pemCert("intermediate-ca"),
				pemCert("leaf"),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := splitPEMChain(tc.input)

			if tc.wantErr != "" {
				if err == nil {
					t.Errorf("\n%s\nsplitPEMChain(...): expected error %q but got nil", tc.reason, tc.wantErr)
					return
				}
				if err.Error() != tc.wantErr {
					t.Errorf("\n%s\nsplitPEMChain(...): -want error, +got error:\n-%s\n+%s", tc.reason, tc.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("\n%s\nsplitPEMChain(...): unexpected error: %v", tc.reason, err)
				return
			}

			if diff := cmp.Diff(tc.wantCerts, got); diff != "" {
				t.Errorf("\n%s\nsplitPEMChain(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestSanitizeIdentifier(t *testing.T) {
	cases := map[string]struct {
		input string
		want  string
	}{
		"AlreadyClean":      {input: "EXAMPLE_CA", want: "EXAMPLE_CA"},
		"Lowercase":         {input: "example-ca", want: "EXAMPLE_CA"},
		"Dots":              {input: "my.ca.cert", want: "MY_CA_CERT"},
		"Spaces":            {input: "my ca cert", want: "MY_CA_CERT"},
		"Mixed":             {input: "AWS-CF-DEL101", want: "AWS_CF_DEL101"},
		"LeadingTrailing":   {input: "-example-", want: "_EXAMPLE_"},
		"Numbers":           {input: "cert2026", want: "CERT2026"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := sanitizeIdentifier(tc.input)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("sanitizeIdentifier(%q): -want, +got:\n%s", tc.input, diff)
			}
		})
	}
}

func TestCertName(t *testing.T) {
	serial := big.NewInt(12345678901234567)
	notBefore := time.Date(2026, 7, 16, 7, 40, 32, 0, time.UTC)
	certPEM := selfSignedPEM(t, serial, notBefore)

	cases := map[string]struct {
		reason  string
		base    string
		pem     []byte
		want    string
		wantErr string
	}{
		"BasicName": {
			reason: "Should produce <SANITIZED_BASE>_CRT_SRV_CERTIFICATE_<serial>_<DDMMYYYYHHMMSS>",
			base:   "aws-cf-del101",
			pem:    certPEM,
			want:   "AWS_CF_DEL101_CRT_SRV_CERTIFICATE_12345678901234567_16072026074032",
		},
		"UppercaseBase": {
			reason: "Uppercase base should produce the same result as lowercase",
			base:   "AWS-CF-DEL101",
			pem:    certPEM,
			want:   "AWS_CF_DEL101_CRT_SRV_CERTIFICATE_12345678901234567_16072026074032",
		},
		"InvalidPEM": {
			reason:  "Non-PEM input should return an error",
			base:    "my-ca",
			pem:     []byte("not pem"),
			wantErr: "failed to decode PEM block for name derivation",
		},
		"InvalidDER": {
			reason:  "A PEM block with invalid DER content should return a parse error",
			base:    "my-ca",
			pem:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not valid der")}),
			wantErr: "failed to parse x509 certificate",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := certName(tc.base, tc.pem)
			if tc.wantErr != "" {
				if err == nil {
					t.Errorf("\n%s\ncertName(...): expected error containing %q but got nil", tc.reason, tc.wantErr)
					return
				}
				if !containsString(err.Error(), tc.wantErr) {
					t.Errorf("\n%s\ncertName(...): want error containing %q, got %q", tc.reason, tc.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("\n%s\ncertName(...): unexpected error: %v", tc.reason, err)
				return
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("\n%s\ncertName(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
