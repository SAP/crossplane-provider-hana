package certificate

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

func splitPEMChain(data []byte) ([][]byte, error) {
	var certificates [][]byte
	for len(bytes.TrimSpace(data)) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			return nil, errors.New("failed to decode PEM certificate")
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("unexpected PEM block type %q", block.Type)
		}
		certificates = append(certificates, pem.EncodeToMemory(block))
		data = rest
	}
	if len(certificates) == 0 {
		return nil, errors.New("no certificates found in PEM chain")
	}
	return certificates, nil
}

// certName derives a HANA certificate name from the x509 certificate and the
// user-supplied base name, following the convention used by the existing
// imperative tooling:
//
//	<BASE>_CRT_SRV_CERTIFICATE_<SERIAL>_<DDMMYYYYHHMMSS>
//
// The base name is uppercased and non-alphanumeric characters are replaced
// with underscores to produce a valid HANA identifier.
func certName(base string, certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("failed to decode PEM block for name derivation")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse x509 certificate: %w", err)
	}

	serial := cert.SerialNumber.String()
	timestamp := cert.NotBefore.UTC().Format("02012006150405")
	sanitized := sanitizeIdentifier(base)

	return fmt.Sprintf("%s_CRT_SRV_CERTIFICATE_%s_%s", sanitized, serial, timestamp), nil
}

// sanitizeIdentifier uppercases the string and replaces any character that is
// not alphanumeric or underscore with an underscore.
func sanitizeIdentifier(s string) string {
	s = strings.ToUpper(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
