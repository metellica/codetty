// Package certs provides certificate authority initialization, certificate
// issuance, revocation, and TLS configuration helpers for CodeTTY mTLS.
//
// All private key files are written with mode 0600.
// The CA is stored as ca.crt / ca.key in the CA directory.
// Server certificate is stored as server.crt / server.key.
// Client certificates are stored as <name>.crt / <name>.key.
// The revocation list is a PEM file at <ca-dir>/crl.pem containing blocks of
// type "CODETTY REVOKED CERTIFICATE" with headers Serial, Name, RevokedAt.
package certs

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// validNameRE matches certificate names that are safe to use as filenames.
var validNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validateName returns an error if name is not a safe filename component.
func validateName(name string) error {
	if !validNameRE.MatchString(name) {
		return fmt.Errorf("certs: invalid certificate name %q: must match ^[A-Za-z0-9._-]+$", name)
	}
	return nil
}

// DefaultCADays is the default validity period for a new CA certificate.
const DefaultCADays = 3650 // ~10 years

// DefaultCertDays is the default validity period for server and client certs.
const DefaultCertDays = 365

// InitCAOptions configures CA and initial server certificate creation.
type InitCAOptions struct {
	// Dir is the directory where ca.crt, ca.key, server.crt, server.key are written.
	Dir string
	// Days is the CA validity in days; zero uses DefaultCADays.
	Days int
	// Hostnames are DNS names / IP addresses for the server certificate SAN.
	// Defaults to ["localhost", "127.0.0.1"] when empty.
	Hostnames []string
}

// IssueOptions configures issuance of a single certificate.
type IssueOptions struct {
	// CADir is the directory containing ca.crt and ca.key.
	CADir string
	// OutDir is the directory where the new cert/key files are written.
	OutDir string
	// Name is used as CommonName and as the file-name prefix.
	Name string
	// Days is the certificate validity; zero uses DefaultCertDays.
	Days int
	// IsServer selects server vs. client key usage.
	IsServer bool
	// Hostnames are DNS/IP SANs for server certificates.
	Hostnames []string
}

// InitCA creates a new CA key pair and a server certificate signed by that CA.
// Files written: ca.crt, ca.key (0600), server.crt, server.key (0600).
func InitCA(opts InitCAOptions) error {
	if err := os.MkdirAll(opts.Dir, 0700); err != nil {
		return fmt.Errorf("certs: mkdir %s: %w", opts.Dir, err)
	}

	// Refuse to overwrite any existing CA/server files.
	for _, f := range []string{"ca.crt", "ca.key", "server.crt", "server.key"} {
		if _, err := os.Stat(filepath.Join(opts.Dir, f)); err == nil {
			return fmt.Errorf("certs: %s already exists; refusing to overwrite", filepath.Join(opts.Dir, f))
		}
	}

	days := opts.Days
	if days == 0 {
		days = DefaultCADays
	}

	// Generate CA key pair.
	caPub, caPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("certs: generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return fmt.Errorf("certs: random serial: %w", err)
	}

	now := time.Now().UTC()
	caTemplate := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "CodeTTY CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Duration(days) * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPub, caPriv)
	if err != nil {
		return fmt.Errorf("certs: create CA cert: %w", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return fmt.Errorf("certs: parse CA cert: %w", err)
	}

	if err := writeCert(filepath.Join(opts.Dir, "ca.crt"), caCertDER); err != nil {
		return err
	}
	if err := writePrivKey(filepath.Join(opts.Dir, "ca.key"), caPriv); err != nil {
		return err
	}

	// Issue server certificate.
	hostnames := opts.Hostnames
	if len(hostnames) == 0 {
		hostnames = []string{"localhost", "127.0.0.1"}
	}
	serverSerial, err := randomSerial()
	if err != nil {
		return fmt.Errorf("certs: random serial: %w", err)
	}

	serverPub, serverPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("certs: generate server key: %w", err)
	}

	serverTemplate := buildServerTemplate("server", serverSerial, now, DefaultCertDays, hostnames)
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, serverPub, caPriv)
	if err != nil {
		return fmt.Errorf("certs: create server cert: %w", err)
	}

	if err := writeCert(filepath.Join(opts.Dir, "server.crt"), serverDER); err != nil {
		return err
	}
	if err := writePrivKey(filepath.Join(opts.Dir, "server.key"), serverPriv); err != nil {
		return err
	}

	return nil
}

// IssueClientCert issues a client certificate signed by the CA in opts.CADir.
func IssueClientCert(opts IssueOptions) error {
	return issueCert(opts, false)
}

// IssueServerCert issues a server certificate signed by the CA in opts.CADir.
func IssueServerCert(opts IssueOptions) error {
	return issueCert(opts, true)
}

func issueCert(opts IssueOptions, isServer bool) error {
	if err := validateName(opts.Name); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.OutDir, 0700); err != nil {
		return fmt.Errorf("certs: mkdir %s: %w", opts.OutDir, err)
	}

	// Refuse to overwrite existing cert/key files.
	for _, f := range []string{opts.Name + ".crt", opts.Name + ".key"} {
		if _, err := os.Stat(filepath.Join(opts.OutDir, f)); err == nil {
			return fmt.Errorf("certs: %s already exists; refusing to overwrite", filepath.Join(opts.OutDir, f))
		}
	}

	days := opts.Days
	if days == 0 {
		days = DefaultCertDays
	}

	// Load CA cert and key.
	caCert, caPriv, err := loadCA(opts.CADir)
	if err != nil {
		return err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("certs: generate key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return fmt.Errorf("certs: random serial: %w", err)
	}

	now := time.Now().UTC()
	var certDER []byte

	if isServer {
		hostnames := opts.Hostnames
		if len(hostnames) == 0 {
			hostnames = []string{"localhost", "127.0.0.1"}
		}
		tmpl := buildServerTemplate(opts.Name, serial, now, days, hostnames)
		certDER, err = x509.CreateCertificate(rand.Reader, tmpl, caCert, pub, caPriv)
	} else {
		tmpl := buildClientTemplate(opts.Name, serial, now, days)
		certDER, err = x509.CreateCertificate(rand.Reader, tmpl, caCert, pub, caPriv)
	}
	if err != nil {
		return fmt.Errorf("certs: create cert: %w", err)
	}

	if err := writeCert(filepath.Join(opts.OutDir, opts.Name+".crt"), certDER); err != nil {
		return err
	}
	if err := writePrivKey(filepath.Join(opts.OutDir, opts.Name+".key"), priv); err != nil {
		return err
	}
	return nil
}

// ---- helpers ----------------------------------------------------------------

func buildServerTemplate(name string, serial *big.Int, now time.Time, days int, hostnames []string) *x509.Certificate {
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hostnames {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	return tmpl
}

func buildClientTemplate(name string, serial *big.Int, now time.Time, days int) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
}

func loadCA(caDir string) (*x509.Certificate, ed25519.PrivateKey, error) {
	certPEM, err := os.ReadFile(filepath.Join(caDir, "ca.crt"))
	if err != nil {
		return nil, nil, fmt.Errorf("certs: read ca.crt: %w", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("certs: ca.crt: no PEM block")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("certs: parse ca.crt: %w", err)
	}

	keyPEM, err := os.ReadFile(filepath.Join(caDir, "ca.key"))
	if err != nil {
		return nil, nil, fmt.Errorf("certs: read ca.key: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("certs: ca.key: no PEM block")
	}
	privKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("certs: parse ca.key: %w", err)
	}
	ed, ok := privKey.(ed25519.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("certs: ca.key: unexpected key type %T", privKey)
	}
	return caCert, ed, nil
}

func randomSerial() (*big.Int, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	// Ensure positive: clear the sign bit of the first byte.
	b[0] &= 0x7f
	// Ensure non-zero.
	b[0] |= 0x01
	return new(big.Int).SetBytes(b), nil
}

func writeCert(path string, derBytes []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("certs: create %s: %w", path, err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		f.Close()
		return fmt.Errorf("certs: encode %s: %w", path, err)
	}
	return f.Close()
}

func writePrivKey(path string, key ed25519.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("certs: marshal private key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("certs: create %s: %w", path, err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		f.Close()
		return fmt.Errorf("certs: encode %s: %w", path, err)
	}
	return f.Close()
}

// CertificateInfo describes a certificate found on disk.
type CertificateInfo struct {
	Path         string
	CommonName   string
	SerialNumber string
	NotAfter     time.Time
	Revoked      bool
}

// ListCertificates scans dir for *.crt files and returns parsed certificate
// information. revoked is the map returned by LoadRevokedSerials; pass nil or
// an empty map if no CRL is available.
func ListCertificates(dir string, revoked map[string]bool) ([]CertificateInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("certs: readdir %s: %w", dir, err)
	}
	var infos []CertificateInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".crt" {
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("certs: read %s: %w", path, err)
		}
		block, _ := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("certs: parse %s: %w", path, err)
		}
		serial := cert.SerialNumber.String()
		infos = append(infos, CertificateInfo{
			Path:         path,
			CommonName:   cert.Subject.CommonName,
			SerialNumber: serial,
			NotAfter:     cert.NotAfter,
			Revoked:      revoked[serial],
		})
	}
	return infos, nil
}

// CertificateUser returns the CommonName from the first certificate in certFile.
func CertificateUser(certFile string) (string, error) {
	raw, err := os.ReadFile(certFile)
	if err != nil {
		return "", fmt.Errorf("certs: read %s: %w", certFile, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", fmt.Errorf("certs: %s: no PEM block", certFile)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("certs: parse %s: %w", certFile, err)
	}
	return cert.Subject.CommonName, nil
}
