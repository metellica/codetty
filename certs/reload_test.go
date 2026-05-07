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

	"codetty/certs"
)

func TestCertReloader_InitialLoad(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	certFile := filepath.Join(caDir, "server.crt")
	keyFile := filepath.Join(caDir, "server.key")
	caFile := filepath.Join(caDir, "ca.crt")

	r, err := certs.NewCertReloader(certFile, keyFile, caFile, "")
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	cfg := r.TLSConfig()
	if cfg == nil {
		t.Fatal("TLSConfig returned nil")
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Error("expected RequireAndVerifyClientCert")
	}
	if cfg.GetCertificate == nil {
		t.Error("expected GetCertificate callback")
	}
	if cfg.GetConfigForClient == nil {
		t.Error("expected GetConfigForClient callback")
	}

	// GetCertificate should return a valid cert
	cert, err := cfg.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatal("GetCertificate returned empty cert")
	}
}

func TestCertReloader_FailsOnBadPaths(t *testing.T) {
	_, err := certs.NewCertReloader("/nonexistent/cert.pem", "/nonexistent/key.pem", "/nonexistent/ca.pem", "")
	if err == nil {
		t.Fatal("expected error for nonexistent paths")
	}
}

func TestCertReloader_ReloadSwapsCert(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	certFile := filepath.Join(caDir, "server.crt")
	keyFile := filepath.Join(caDir, "server.key")
	caFile := filepath.Join(caDir, "ca.crt")

	r, err := certs.NewCertReloader(certFile, keyFile, caFile, "")
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	cfg := r.TLSConfig()
	cert1, _ := cfg.GetCertificate(nil)

	// Remove old cert/key so IssueServerCert can create new ones.
	os.Remove(certFile)
	os.Remove(keyFile)

	// Issue a new server cert
	if err := certs.IssueServerCert(certs.IssueOptions{
		CADir:     caDir,
		OutDir:    caDir,
		Name:      "server",
		Days:      5,
		Hostnames: []string{"localhost"},
	}); err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}

	// Before reload, old cert is still served
	cert2, _ := cfg.GetCertificate(nil)
	if !certBytesEqual(cert1, cert2) {
		t.Error("cert should not change before Reload()")
	}

	// Reload
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// After reload, new cert is served
	cert3, _ := cfg.GetCertificate(nil)
	if certBytesEqual(cert1, cert3) {
		t.Error("cert should change after Reload() with new server cert")
	}
}

func TestCertReloader_ReloadFailureKeepsOld(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	certFile := filepath.Join(caDir, "server.crt")
	keyFile := filepath.Join(caDir, "server.key")
	caFile := filepath.Join(caDir, "ca.crt")

	r, err := certs.NewCertReloader(certFile, keyFile, caFile, "")
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	cfg := r.TLSConfig()
	certBefore, _ := cfg.GetCertificate(nil)

	// Corrupt the cert file
	os.WriteFile(certFile, []byte("garbage"), 0644)

	// Reload should fail
	if err := r.Reload(); err == nil {
		t.Fatal("expected Reload to fail with corrupt cert")
	}

	// Old cert should still be in effect
	certAfter, _ := cfg.GetCertificate(nil)
	if !certBytesEqual(certBefore, certAfter) {
		t.Error("cert should remain unchanged after failed reload")
	}
}

func TestCertReloader_HandshakeWithReload(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	// Issue a client cert
	if err := certs.IssueClientCert(certs.IssueOptions{
		CADir:  caDir,
		OutDir: caDir,
		Name:   "testclient",
		Days:   5,
	}); err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	certFile := filepath.Join(caDir, "server.crt")
	keyFile := filepath.Join(caDir, "server.key")
	caFile := filepath.Join(caDir, "ca.crt")

	r, err := certs.NewCertReloader(certFile, keyFile, caFile, "")
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	// Start a TLS server using the reloader
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	tlsLn := tls.NewListener(ln, r.TLSConfig())
	defer tlsLn.Close()

	// Server goroutine: accept one connection, read, echo back
	serverDone := make(chan error, 1)
	go func() {
		conn, err := tlsLn.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		if err != nil {
			serverDone <- err
			return
		}
		conn.Write(buf[:n])
		serverDone <- nil
	}()

	// Client connects with mTLS
	clientCert, err := tls.LoadX509KeyPair(
		filepath.Join(caDir, "testclient.crt"),
		filepath.Join(caDir, "testclient.key"),
	)
	if err != nil {
		t.Fatalf("load client cert: %v", err)
	}
	caRaw, _ := os.ReadFile(caFile)
	clientCAPool := x509.NewCertPool()
	clientCAPool.AppendCertsFromPEM(caRaw)

	clientCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      clientCAPool,
		ServerName:   "localhost",
	}

	conn, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("client write: %v", err)
	}
	resp := make([]byte, 64)
	n, err := conn.Read(resp)
	if err != nil && err != io.EOF {
		t.Fatalf("client read: %v", err)
	}
	if string(resp[:n]) != "hello" {
		t.Fatalf("expected 'hello', got %q", resp[:n])
	}

	if err := <-serverDone; err != nil {
		t.Fatalf("server error: %v", err)
	}

	// Reload should not break subsequent connections
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload after handshake: %v", err)
	}
}

func TestCertReloader_CRLRevocationCheck(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	if err := certs.IssueClientCert(certs.IssueOptions{
		CADir:  caDir,
		OutDir: caDir,
		Name:   "revokeme",
		Days:   5,
	}); err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	crlFile := filepath.Join(caDir, "crl.pem")
	certFile := filepath.Join(caDir, "server.crt")
	keyFile := filepath.Join(caDir, "server.key")
	caFile := filepath.Join(caDir, "ca.crt")

	r, err := certs.NewCertReloader(certFile, keyFile, caFile, crlFile)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	// Client config
	clientCert, _ := tls.LoadX509KeyPair(
		filepath.Join(caDir, "revokeme.crt"),
		filepath.Join(caDir, "revokeme.key"),
	)
	caRaw, _ := os.ReadFile(caFile)
	clientCAPool := x509.NewCertPool()
	clientCAPool.AppendCertsFromPEM(caRaw)
	clientCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      clientCAPool,
		ServerName:   "localhost",
	}

	// Helper: attempt a handshake with the reloader's TLSConfig
	doReloaderHandshake := func(expectFail bool) {
		t.Helper()
		ln, err := tls.Listen("tcp", "127.0.0.1:0", r.TLSConfig())
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
			err = conn.(*tls.Conn).Handshake()
			serverDone <- result{err}
		}()

		conn, err := tls.Dial("tcp", ln.Addr().(*net.TCPAddr).String(), clientCfg)
		if err != nil {
			if expectFail {
				<-serverDone
				return
			}
			t.Fatalf("client dial: %v", err)
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("ping"))
		conn.Close()

		sr := <-serverDone
		if expectFail && sr.err == nil {
			t.Error("expected server handshake to fail for revoked cert, but it succeeded")
		}
		if !expectFail && sr.err != nil {
			t.Errorf("server handshake error: %v", sr.err)
		}
	}

	// Before revocation: handshake should succeed
	doReloaderHandshake(false)

	// Revoke the cert
	if err := certs.RevokeCertificate(caDir, "revokeme"); err != nil {
		t.Fatalf("RevokeCertificate: %v", err)
	}

	// After revocation: handshake should fail (CRL is re-read per handshake)
	doReloaderHandshake(true)
}

// certBytesEqual checks if two tls.Certificate references point to the same raw bytes.
func certBytesEqual(a, b *tls.Certificate) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Certificate) != len(b.Certificate) {
		return false
	}
	for i := range a.Certificate {
		if len(a.Certificate[i]) != len(b.Certificate[i]) {
			return false
		}
		for j := range a.Certificate[i] {
			if a.Certificate[i][j] != b.Certificate[i][j] {
				return false
			}
		}
	}
	return true
}

// Ensure IssueServerCert is available (it's in ca.go)
func TestCertReloader_LogStatus(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	r, err := certs.NewCertReloader(
		filepath.Join(caDir, "server.crt"),
		filepath.Join(caDir, "server.key"),
		filepath.Join(caDir, "ca.crt"),
		"",
	)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	// LogStatus should not panic
	r.LogStatus()
}

// Verify the leaf cert from GetConfigForClient callback contains the CA pool
func TestCertReloader_GetConfigForClient_HasCAPool(t *testing.T) {
	caDir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: caDir, Days: 10}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	r, err := certs.NewCertReloader(
		filepath.Join(caDir, "server.crt"),
		filepath.Join(caDir, "server.key"),
		filepath.Join(caDir, "ca.crt"),
		"",
	)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	cfg := r.TLSConfig()
	childCfg, err := cfg.GetConfigForClient(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if childCfg.ClientCAs == nil {
		t.Error("expected ClientCAs in child config")
	}

	// Read the CA cert and verify it's in the pool
	caRaw, _ := os.ReadFile(filepath.Join(caDir, "ca.crt"))
	block, _ := pem.Decode(caRaw)
	caCert, _ := x509.ParseCertificate(block.Bytes)

	subjects := childCfg.ClientCAs.Subjects()
	found := false
	for _, s := range subjects {
		if string(s) == string(caCert.RawSubject) {
			found = true
			break
		}
	}
	if !found {
		t.Error("CA cert not found in child config's ClientCAs pool")
	}
}
