package certs_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codetty/certs"
)

func TestInitCA_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	err := certs.InitCA(certs.InitCAOptions{Dir: dir, Days: 10})
	if err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	// CA cert and key must exist
	for _, f := range []string{"ca.crt", "ca.key", "server.crt", "server.key"} {
		path := filepath.Join(dir, f)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
			continue
		}
		// Private keys must be mode 0600
		if f == "ca.key" || f == "server.key" {
			if mode := info.Mode().Perm(); mode != 0600 {
				t.Errorf("%s: expected mode 0600, got %o", f, mode)
			}
		}
	}
}

func TestInitCA_CACertIsCert(t *testing.T) {
	dir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: dir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatalf("read ca.crt: %v", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("ca.crt is not a PEM CERTIFICATE block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse ca.crt: %v", err)
	}
	if !cert.IsCA {
		t.Error("ca.crt: IsCA should be true")
	}
	if cert.NotAfter.Before(time.Now().Add(9 * 24 * time.Hour)) {
		t.Errorf("ca.crt: NotAfter too soon: %v", cert.NotAfter)
	}
}

func TestIssueClientCert_CreatesFilesWithCN(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	outDir := t.TempDir()
	err := certs.IssueClientCert(certs.IssueOptions{
		CADir:  caDir,
		OutDir: outDir,
		Name:   "alice",
		Days:   5,
	})
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	// Check files exist with correct permissions
	for _, f := range []string{"alice.crt", "alice.key"} {
		path := filepath.Join(outDir, f)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s: %v", f, err)
			continue
		}
		if f == "alice.key" {
			if mode := info.Mode().Perm(); mode != 0600 {
				t.Errorf("%s: expected mode 0600, got %o", f, mode)
			}
		}
	}

	// Parse cert and check CN and key usage
	raw, _ := os.ReadFile(filepath.Join(outDir, "alice.crt"))
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("alice.crt: no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse alice.crt: %v", err)
	}
	if cert.Subject.CommonName != "alice" {
		t.Errorf("CN: want alice, got %s", cert.Subject.CommonName)
	}
	hasClientAuth := false
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
		}
	}
	if !hasClientAuth {
		t.Error("alice.crt: missing ExtKeyUsageClientAuth")
	}
}

func TestIssueServerCert_HasServerAuth(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	outDir := caDir // server cert goes into the CA dir by convention
	err := certs.IssueServerCert(certs.IssueOptions{
		CADir:     caDir,
		OutDir:    outDir,
		Name:      "server",
		Days:      5,
		Hostnames: []string{"localhost"},
	})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}

	raw, _ := os.ReadFile(filepath.Join(outDir, "server.crt"))
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("server.crt: no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse server.crt: %v", err)
	}
	hasServerAuth := false
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
		}
	}
	if !hasServerAuth {
		t.Error("server.crt: missing ExtKeyUsageServerAuth")
	}
}

func TestRevokeCertificate_AndLoadRevokedSerials(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	outDir := t.TempDir()
	err := certs.IssueClientCert(certs.IssueOptions{
		CADir:  caDir,
		OutDir: outDir,
		Name:   "bob",
		Days:   5,
	})
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	// Copy bob.crt into caDir so RevokeCertificate can find it by name
	bobCrt, _ := os.ReadFile(filepath.Join(outDir, "bob.crt"))
	_ = os.WriteFile(filepath.Join(caDir, "bob.crt"), bobCrt, 0644)

	if err := certs.RevokeCertificate(caDir, "bob"); err != nil {
		t.Fatalf("RevokeCertificate: %v", err)
	}

	crlPath := filepath.Join(caDir, "crl.pem")
	revoked, err := certs.LoadRevokedSerials(crlPath)
	if err != nil {
		t.Fatalf("LoadRevokedSerials: %v", err)
	}
	if len(revoked) == 0 {
		t.Fatal("expected at least one revoked serial")
	}

	// Get bob's serial directly
	block, _ := pem.Decode(bobCrt)
	cert, _ := x509.ParseCertificate(block.Bytes)
	serial := cert.SerialNumber.String()
	if !revoked[serial] {
		t.Errorf("bob's serial %s not found in revoked map", serial)
	}
}

func TestLoadRevokedSerials_MissingFile(t *testing.T) {
	revoked, err := certs.LoadRevokedSerials("/nonexistent/path/crl.pem")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(revoked) != 0 {
		t.Errorf("expected empty map for missing file, got %d entries", len(revoked))
	}
}

func TestListCertificates(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	outDir := t.TempDir()
	for _, name := range []string{"alice", "bob"} {
		if err := certs.IssueClientCert(certs.IssueOptions{
			CADir:  caDir,
			OutDir: outDir,
			Name:   name,
			Days:   5,
		}); err != nil {
			t.Fatalf("IssueClientCert(%s): %v", name, err)
		}
	}

	// Revoke bob
	bobCrt, _ := os.ReadFile(filepath.Join(outDir, "bob.crt"))
	_ = os.WriteFile(filepath.Join(caDir, "bob.crt"), bobCrt, 0644)
	if err := certs.RevokeCertificate(caDir, "bob"); err != nil {
		t.Fatalf("RevokeCertificate: %v", err)
	}
	revoked, _ := certs.LoadRevokedSerials(filepath.Join(caDir, "crl.pem"))

	infos, err := certs.ListCertificates(outDir, revoked)
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 cert infos, got %d", len(infos))
	}

	byName := map[string]certs.CertificateInfo{}
	for _, info := range infos {
		byName[info.CommonName] = info
	}
	if byName["alice"].Revoked {
		t.Error("alice should not be revoked")
	}
	if !byName["bob"].Revoked {
		t.Error("bob should be revoked")
	}
}

func TestBuildTLSConfigs_Handshake(t *testing.T) {
	dir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: dir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	// Issue a client cert
	if err := certs.IssueClientCert(certs.IssueOptions{
		CADir:  dir,
		OutDir: dir,
		Name:   "testuser",
		Days:   5,
	}); err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	caFile := filepath.Join(dir, "ca.crt")
	serverCert := filepath.Join(dir, "server.crt")
	serverKey := filepath.Join(dir, "server.key")
	clientCert := filepath.Join(dir, "testuser.crt")
	clientKey := filepath.Join(dir, "testuser.key")
	crlFile := filepath.Join(dir, "crl.pem") // doesn't exist yet → no revocations

	serverCfg, err := certs.BuildServerTLSConfig(serverCert, serverKey, caFile, crlFile)
	if err != nil {
		t.Fatalf("BuildServerTLSConfig: %v", err)
	}
	clientCfg, err := certs.BuildClientTLSConfig(clientCert, clientKey, caFile)
	if err != nil {
		t.Fatalf("BuildClientTLSConfig: %v", err)
	}
	clientCfg.ServerName = "localhost"

	doHandshake(t, serverCfg, clientCfg, false)
}

func TestBuildServerTLSConfig_RejectsRevokedClient(t *testing.T) {
	dir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: dir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	if err := certs.IssueClientCert(certs.IssueOptions{
		CADir:  dir,
		OutDir: dir,
		Name:   "revoked",
		Days:   5,
	}); err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	// Revoke the cert
	if err := certs.RevokeCertificate(dir, "revoked"); err != nil {
		t.Fatalf("RevokeCertificate: %v", err)
	}

	caFile := filepath.Join(dir, "ca.crt")
	serverCert := filepath.Join(dir, "server.crt")
	serverKey := filepath.Join(dir, "server.key")
	clientCert := filepath.Join(dir, "revoked.crt")
	clientKey := filepath.Join(dir, "revoked.key")
	crlFile := filepath.Join(dir, "crl.pem")

	serverCfg, err := certs.BuildServerTLSConfig(serverCert, serverKey, caFile, crlFile)
	if err != nil {
		t.Fatalf("BuildServerTLSConfig: %v", err)
	}
	clientCfg, err := certs.BuildClientTLSConfig(clientCert, clientKey, caFile)
	if err != nil {
		t.Fatalf("BuildClientTLSConfig: %v", err)
	}
	clientCfg.ServerName = "localhost"

	doHandshake(t, serverCfg, clientCfg, true /* expectFailure */)
}

func TestCertificateUser(t *testing.T) {
	dir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: dir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	if err := certs.IssueClientCert(certs.IssueOptions{
		CADir:  dir,
		OutDir: dir,
		Name:   "charlie",
		Days:   5,
	}); err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	user, err := certs.CertificateUser(filepath.Join(dir, "charlie.crt"))
	if err != nil {
		t.Fatalf("CertificateUser: %v", err)
	}
	if user != "charlie" {
		t.Errorf("want charlie, got %s", user)
	}
}

// doHandshake starts a listener with serverCfg, connects with clientCfg,
// and verifies the handshake either succeeds or fails as expected.
func doHandshake(t *testing.T, serverCfg, clientCfg *tls.Config, expectFailure bool) {
	t.Helper()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer ln.Close()

	type result struct{ err error }
	serverDone := make(chan result, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverDone <- result{err}
			return
		}
		defer conn.Close()
		tlsConn := conn.(*tls.Conn)
		err = tlsConn.Handshake()
		serverDone <- result{err}
	}()

	clientConn, err := tls.Dial("tcp", ln.Addr().(*net.TCPAddr).String(), clientCfg)
	if err != nil {
		if expectFailure {
			// Drain server goroutine
			<-serverDone
			return
		}
		t.Fatalf("client dial: %v", err)
	}
	defer clientConn.Close()

	// Write something so server completes handshake
	_, _ = io.WriteString(clientConn, "ping")
	clientConn.Close()

	sr := <-serverDone
	if expectFailure {
		if sr.err == nil {
			t.Error("expected server handshake to fail for revoked cert, but it succeeded")
		}
	} else {
		if sr.err != nil {
			t.Errorf("server handshake error: %v", sr.err)
		}
	}
}
