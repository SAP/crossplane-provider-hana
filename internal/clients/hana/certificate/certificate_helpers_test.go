package certificate

import (
	"encoding/pem"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func pemCert(content string) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte(content)})
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
