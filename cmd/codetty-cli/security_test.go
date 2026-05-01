package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codetty/certs"
	"codetty/protocol"

	"github.com/gorilla/websocket"
)

// TestPrepareConnectionConfig_SecureWithoutCerts verifies that secure mode
// (default) without any cert/key/ca flags returns a helpful error naming the
// missing flags.
func TestPrepareConnectionConfig_SecureWithoutCerts(t *testing.T) {
	cfg := clientConfig{
		ServerURL: "wss://localhost:8080/ws",
		Insecure:  false,
	}
	_, err := prepareConnectionConfig(cfg)
	if err == nil {
		t.Fatal("expected error for secure mode without certs")
	}
	errMsg := err.Error()
	for _, keyword := range []string{"--cert", "--key", "--ca", "--insecure"} {
		if !strings.Contains(errMsg, keyword) {
			t.Errorf("error message should mention %q, got: %s", keyword, errMsg)
		}
	}
}

// TestPrepareConnectionConfig_InsecureDefaultServer verifies that insecure mode
// produces a plain websocket dialer and no User.
func TestPrepareConnectionConfig_InsecureDefaultServer(t *testing.T) {
	cfg := clientConfig{
		ServerURL: "ws://localhost:8080/ws",
		Insecure:  true,
	}
	connCfg, err := prepareConnectionConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connCfg.User != "" {
		t.Errorf("insecure mode should have empty User, got %q", connCfg.User)
	}
	if connCfg.Dialer == nil {
		t.Fatal("dialer should not be nil")
	}
	if connCfg.ServerURL != "ws://localhost:8080/ws" {
		t.Errorf("ServerURL = %q, want ws://localhost:8080/ws", connCfg.ServerURL)
	}
}

// TestPrepareConnectionConfig_SecureWithCerts verifies that a valid cert/key/ca
// combination produces a dialer with TLS config and extracts the CN as User.
func TestPrepareConnectionConfig_SecureWithCerts(t *testing.T) {
	dir := t.TempDir()
	if err := certs.InitCA(certs.InitCAOptions{Dir: dir}); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	if err := certs.IssueClientCert(certs.IssueOptions{
		CADir:  dir,
		OutDir: dir,
		Name:   "alice",
	}); err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}

	cfg := clientConfig{
		ServerURL: "wss://localhost:8080/ws",
		CertFile:  filepath.Join(dir, "alice.crt"),
		KeyFile:   filepath.Join(dir, "alice.key"),
		CAFile:    filepath.Join(dir, "ca.crt"),
		Insecure:  false,
	}
	connCfg, err := prepareConnectionConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connCfg.User != "alice" {
		t.Errorf("User = %q, want %q", connCfg.User, "alice")
	}
	if connCfg.Dialer == nil {
		t.Fatal("dialer should not be nil")
	}
	if connCfg.Dialer.TLSClientConfig == nil {
		t.Fatal("dialer TLSClientConfig should not be nil for secure mode")
	}
	if connCfg.ServerURL != "wss://localhost:8080/ws" {
		t.Errorf("ServerURL = %q, want wss://localhost:8080/ws", connCfg.ServerURL)
	}
}

// TestApplyCertDir verifies that --cert-dir fills in client.crt, client.key,
// ca.crt, and that direct flag values override cert-dir values.
func TestApplyCertDir(t *testing.T) {
	dir := "/some/dir"

	// cert-dir fills in all missing fields
	cfg := clientConfig{CertDir: dir}
	applyCertDir(&cfg)
	if cfg.CertFile != filepath.Join(dir, "client.crt") {
		t.Errorf("CertFile = %q, want %q", cfg.CertFile, filepath.Join(dir, "client.crt"))
	}
	if cfg.KeyFile != filepath.Join(dir, "client.key") {
		t.Errorf("KeyFile = %q, want %q", cfg.KeyFile, filepath.Join(dir, "client.key"))
	}
	if cfg.CAFile != filepath.Join(dir, "ca.crt") {
		t.Errorf("CAFile = %q, want %q", cfg.CAFile, filepath.Join(dir, "ca.crt"))
	}

	// direct flags override cert-dir
	cfg2 := clientConfig{
		CertDir:  dir,
		CertFile: "/override/my.crt",
		KeyFile:  "/override/my.key",
		CAFile:   "/override/myca.crt",
	}
	applyCertDir(&cfg2)
	if cfg2.CertFile != "/override/my.crt" {
		t.Errorf("CertFile should not be overridden, got %q", cfg2.CertFile)
	}
	if cfg2.KeyFile != "/override/my.key" {
		t.Errorf("KeyFile should not be overridden, got %q", cfg2.KeyFile)
	}
	if cfg2.CAFile != "/override/myca.crt" {
		t.Errorf("CAFile should not be overridden, got %q", cfg2.CAFile)
	}
}

// TestParseClientFlags_InsecureDefaultServer verifies that --insecure with no
// explicit --server substitutes ws:// for the default wss:// URL.
func TestParseClientFlags_InsecureDefaultServer(t *testing.T) {
	cfg, remaining, err := parseClientFlags([]string{"--insecure", "--", "bash"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Insecure {
		t.Fatal("expected Insecure=true")
	}
	if cfg.ServerURL != "ws://localhost:8080/ws" {
		t.Errorf("ServerURL = %q, want ws://localhost:8080/ws", cfg.ServerURL)
	}
	if len(remaining) == 0 || remaining[0] != "bash" {
		t.Errorf("remaining = %v, want [bash]", remaining)
	}
}

// TestParseClientFlags_CertDirAndOverride verifies --cert-dir and direct flag
// interactions via parseClientFlags+applyCertDir.
func TestParseClientFlags_CertDirAndOverride(t *testing.T) {
	dir := "/certs/dir"

	// cert-dir only
	cfg, _, err := parseClientFlags([]string{"--cert-dir", dir, "--insecure", "--", "bash"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CertDir != dir {
		t.Errorf("CertDir = %q, want %q", cfg.CertDir, dir)
	}
	// applyCertDir should fill in values
	applyCertDir(&cfg)
	if cfg.CertFile != filepath.Join(dir, "client.crt") {
		t.Errorf("CertFile = %q after applyCertDir", cfg.CertFile)
	}

	// direct cert overrides cert-dir
	cfg2, _, err := parseClientFlags([]string{
		"--cert-dir", dir,
		"--cert", "/direct/me.crt",
		"--insecure", "--", "bash",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	applyCertDir(&cfg2)
	if cfg2.CertFile != "/direct/me.crt" {
		t.Errorf("CertFile = %q, want /direct/me.crt", cfg2.CertFile)
	}
}

// TestRunConnection_SendsUser verifies that runConnection includes
// connectionConfig.User in the RegisterPayload sent to the hub.
func TestRunConnection_SendsUser(t *testing.T) {
	gotUser := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Upgrade(w, r, nil, 1024, 1024)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		msgType, payload, ok := protocol.ParseType(msg)
		if !ok || msgType != protocol.MsgRegister {
			gotUser <- ""
			return
		}
		var reg protocol.RegisterPayload
		if err := json.Unmarshal(payload, &reg); err != nil {
			gotUser <- ""
			return
		}
		gotUser <- reg.User
		// Keep connection alive until the test signals done via doneCh.
		<-gotUser
	}))
	defer server.Close()

	term := newFakeTerminal()
	screen := NewScreenMirror(80, 24)
	outputCh := make(chan []byte)
	doneCh := make(chan error, 1)
	var replayMu sync.Mutex

	connCfg := connectionConfig{
		ServerURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		Dialer:    websocket.DefaultDialer,
		User:      "testuser",
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runConnection(connCfg, "id", "title", term, outputCh, doneCh, screen, &replayMu)
	}()

	var user string
	select {
	case user = <-gotUser:
		if user != "testuser" {
			t.Errorf("RegisterPayload.User = %q, want %q", user, "testuser")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for register message")
	}

	// Signal process exit so runConnection returns cleanly.
	doneCh <- nil
	// Unblock the server handler.
	gotUser <- ""

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("runConnection returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runConnection did not return after doneCh signal")
	}
}

// TestPrepareConnectionConfig_SecureRejectsWsScheme verifies that secure mode
// (default) rejects a ws:// server URL with a clear error.
func TestPrepareConnectionConfig_SecureRejectsWsScheme(t *testing.T) {
	cfg := clientConfig{
		ServerURL: "ws://localhost:8080/ws",
		Insecure:  false,
	}
	_, err := prepareConnectionConfig(cfg)
	if err == nil {
		t.Fatal("expected error for secure mode with ws:// URL")
	}
	if !strings.Contains(err.Error(), "wss://") {
		t.Errorf("error should mention wss://, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "--insecure") {
		t.Errorf("error should mention --insecure, got: %s", err.Error())
	}
}

// TestPrepareConnectionConfig_InsecureRejectsWssScheme verifies that insecure
// mode rejects a wss:// server URL.
func TestPrepareConnectionConfig_InsecureRejectsWssScheme(t *testing.T) {
	cfg := clientConfig{
		ServerURL: "wss://localhost:8080/ws",
		Insecure:  true,
	}
	_, err := prepareConnectionConfig(cfg)
	if err == nil {
		t.Fatal("expected error for insecure mode with wss:// URL")
	}
	if !strings.Contains(err.Error(), "ws://") {
		t.Errorf("error should mention ws://, got: %s", err.Error())
	}
}

// TestPrepareConnectionConfig_InsecureRejectsCertFlags verifies that --insecure
// combined with any cert flag returns a diagnostic error.
func TestPrepareConnectionConfig_InsecureRejectsCertFlags(t *testing.T) {
	base := clientConfig{ServerURL: "ws://localhost:8080/ws", Insecure: true}

	cases := []struct {
		name string
		cfg  clientConfig
	}{
		{"cert", clientConfig{ServerURL: base.ServerURL, Insecure: true, CertFile: "c.crt"}},
		{"key", clientConfig{ServerURL: base.ServerURL, Insecure: true, KeyFile: "c.key"}},
		{"ca", clientConfig{ServerURL: base.ServerURL, Insecure: true, CAFile: "ca.crt"}},
		{"cert-dir", clientConfig{ServerURL: base.ServerURL, Insecure: true, CertDir: "/some/dir"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := prepareConnectionConfig(tc.cfg)
			if err == nil {
				t.Fatalf("expected error for --insecure with %s flag", tc.name)
			}
			if !strings.Contains(err.Error(), "--insecure") {
				t.Errorf("error should mention --insecure, got: %s", err.Error())
			}
		})
	}
}

// TestRunConnection_AuthRejected verifies that when the server sends a
// ClosePolicyViolation frame with reason "codetty auth rejected", runConnection
// returns an error satisfying errors.Is(err, errAuthRejected).
func TestRunConnection_AuthRejected(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Read the register message (discard it).
		conn.SetReadDeadline(time.Now().Add(time.Second))
		conn.ReadMessage()
		// Send policy-violation close.
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "codetty auth rejected: alice != mallory"))
		// Drain until client closes.
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		conn.ReadMessage()
	}))
	defer server.Close()

	term := newFakeTerminal()
	screen := NewScreenMirror(80, 24)
	outputCh := make(chan []byte)
	doneCh := make(chan error, 1)
	var replayMu sync.Mutex

	connCfg := connectionConfig{
		ServerURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		Dialer:    websocket.DefaultDialer,
		User:      "alice",
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runConnection(connCfg, "id", "title", term, outputCh, doneCh, screen, &replayMu)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("runConnection should return an error on auth rejection, got nil")
		}
		if !errors.Is(err, errAuthRejected) {
			t.Errorf("expected errors.Is(err, errAuthRejected), got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runConnection did not return within timeout")
	}
}

// TestIsFatalConnectionError verifies that errAuthRejected is fatal and that a
// generic error is not.
func TestIsFatalConnectionError(t *testing.T) {
	if !isFatalConnectionError(errAuthRejected) {
		t.Error("errAuthRejected should be a fatal connection error")
	}
	if !isFatalConnectionError(fmt.Errorf("wrapped: %w", errAuthRejected)) {
		t.Error("wrapped errAuthRejected should be a fatal connection error")
	}
	if isFatalConnectionError(errors.New("transient network error")) {
		t.Error("a generic error should not be fatal")
	}
	if isFatalConnectionError(nil) {
		t.Error("nil should not be fatal")
	}
}
