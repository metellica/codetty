package certs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"encoding/pem"
)

const revokedBlockType = "CODETTY REVOKED CERTIFICATE"

// RevokeCertificate adds the certificate named <name>.crt in caDir to the
// revocation list at <caDir>/crl.pem.
func RevokeCertificate(caDir, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	certPath := filepath.Join(caDir, name+".crt")
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("certs: read %s: %w", certPath, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return fmt.Errorf("certs: %s: no PEM block", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("certs: parse %s: %w", certPath, err)
	}

	revokedBlock := &pem.Block{
		Type: revokedBlockType,
		Headers: map[string]string{
			"Serial":    cert.SerialNumber.String(),
			"Name":      name,
			"RevokedAt": time.Now().UTC().Format(time.RFC3339),
		},
		Bytes: cert.Raw,
	}

	crlPath := filepath.Join(caDir, "crl.pem")
	f, err := os.OpenFile(crlPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("certs: open crl.pem: %w", err)
	}
	if err := pem.Encode(f, revokedBlock); err != nil {
		f.Close()
		return fmt.Errorf("certs: write crl.pem: %w", err)
	}
	return f.Close()
}

// LoadRevokedSerials parses the CRL PEM file at path and returns a set of
// revoked serial number strings. If the file does not exist, an empty map and
// nil error are returned.
func LoadRevokedSerials(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("certs: read crl %s: %w", path, err)
	}

	revoked := map[string]bool{}
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != revokedBlockType {
			continue
		}
		serial := strings.TrimSpace(block.Headers["Serial"])
		if serial != "" {
			revoked[serial] = true
		}
	}
	return revoked, nil
}

// BuildServerTLSConfig creates a *tls.Config for the hub server. The CRL file
// is re-read on every new TLS client handshake so that certificates revoked
// after the config was built are rejected immediately. Pass an empty or
// non-existent crlFile to skip revocation checks.
// Clients must present a valid certificate signed by the CA.
func BuildServerTLSConfig(certFile, keyFile, caFile, crlFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("certs: load server keypair: %w", err)
	}

	caPool, err := loadCAPool(caFile)
	if err != nil {
		return nil, err
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return nil
			}
			// Re-read the CRL on each handshake so runtime revocations are observed.
			revoked, err := LoadRevokedSerials(crlFile)
			if err != nil {
				return fmt.Errorf("certs: read CRL: %w", err)
			}
			// Check only the leaf cert (index 0).
			c, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("certs: parse peer cert: %w", err)
			}
			if revoked[c.SerialNumber.String()] {
				return fmt.Errorf("certs: certificate %s (%s) has been revoked",
					c.Subject.CommonName, serialHex(c.SerialNumber))
			}
			return nil
		},
	}
	return cfg, nil
}

// BuildClientTLSConfig creates a *tls.Config for the CLI client.
func BuildClientTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("certs: load client keypair: %w", err)
	}

	caPool, err := loadCAPool(caFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
	}, nil
}

// ---- helpers ----------------------------------------------------------------

func loadCAPool(caFile string) (*x509.CertPool, error) {
	raw, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("certs: read CA file %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("certs: no valid CA certificates in %s", caFile)
	}
	return pool, nil
}

func serialHex(s *big.Int) string {
	return fmt.Sprintf("%X", s.Bytes())
}
