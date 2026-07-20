package certificate

import (
    "bytes"
    "encoding/pem"
    "fmt"
	"errors"
)

func splitPEMChain(data []byte) ([][]byte, error) {
    var certificates [][]byte
    for {
        if len(bytes.TrimSpace(data)) == 0 {
            break
        }
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

func pemToString(cert []byte) string {
    return string(cert)
}