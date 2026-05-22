package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"

	"codetty/certs"
	"codetty/hub"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type hubConfig struct {
	Addr     string
	CertFile string
	KeyFile  string
	CAFile   string
	CRLFile  string
	Insecure bool
}

func cmdHub(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "init-ca":
			if err := runInitCA(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "issue-cert":
			if err := runIssueCert(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "list-certs":
			out, err := runListCerts(args[1:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(out)
			return
		case "revoke-cert":
			if err := runRevokeCert(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	cfg, err := parseHubFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if cfg.Insecure {
		log.Printf("WARNING: running in insecure ws:// mode — mTLS is disabled")
		log.Printf("WARNING: all connections are unauthenticated; use only in trusted environments")
	}

	var reloader *certs.CertReloader
	if !cfg.Insecure {
		reloader, err = certs.NewCertReloader(cfg.CertFile, cfg.KeyFile, cfg.CAFile, cfg.CRLFile)
		if err != nil {
			log.Fatalf("tls init: %v", err)
		}
		reloader.LogStatus()
	}

	h := hub.New()
	model := hub.NewModel(h)

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
	h.SetProgram(p)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("upgrade error: %v", err)
			return
		}
		auth := hub.AuthInfo{}
		if !cfg.Insecure && r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			auth.Secure = true
			auth.User = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		go h.HandleConn(conn, auth)
	})

	ln, err := buildHubListener(cfg, reloader)
	if err != nil {
		log.Fatalf("listener: %v", err)
	}

	srv := &http.Server{Handler: mux}

	go func() {
		scheme := "wss"
		if cfg.Insecure {
			scheme = "ws"
		}
		log.Printf("codetty hub listening on %s://%s/ws", scheme, cfg.Addr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGHUP:
				if reloader == nil {
					log.Printf("[signal] SIGHUP received but running in insecure mode; nothing to reload")
					continue
				}
				log.Printf("[signal] SIGHUP received, reloading TLS certificates...")
				if err := reloader.Reload(); err != nil {
					log.Printf("[signal] TLS reload FAILED (keeping old certs): %v", err)
				} else {
					log.Printf("[signal] TLS reload successful")
					reloader.LogStatus()
				}
			case syscall.SIGTERM, syscall.SIGINT:
				log.Printf("[signal] %s received, shutting down gracefully...", sig)
				if err := srv.Shutdown(context.Background()); err != nil {
					log.Printf("[signal] HTTP shutdown error: %v", err)
				}
				p.Quit()
				return
			}
		}
	}()

	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}

func parseHubFlags(args []string) (hubConfig, error) {
	fs := flag.NewFlagSet("codetty hub", flag.ContinueOnError)
	cfg := hubConfig{}
	fs.StringVar(&cfg.Addr, "addr", ":8080", "listen address")
	fs.StringVar(&cfg.CertFile, "cert", "", "TLS server certificate file")
	fs.StringVar(&cfg.KeyFile, "key", "", "TLS server key file")
	fs.StringVar(&cfg.CAFile, "ca", "", "CA certificate file for client verification")
	fs.StringVar(&cfg.CRLFile, "crl", "", "CRL PEM file for certificate revocation (optional)")
	fs.BoolVar(&cfg.Insecure, "insecure", false, "disable mTLS and use plain ws:// (insecure)")
	if err := fs.Parse(args); err != nil {
		return hubConfig{}, err
	}
	if !cfg.Insecure {
		if cfg.CertFile == "" || cfg.KeyFile == "" || cfg.CAFile == "" {
			return hubConfig{}, fmt.Errorf(
				"codetty hub requires --cert, --key, and --ca for secure (mTLS) mode\n" +
					"  Generate certificates with: codetty hub init-ca\n" +
					"  Or start without mTLS using: codetty hub --insecure",
			)
		}
	}
	return cfg, nil
}

func buildHubListener(cfg hubConfig, reloader *certs.CertReloader) (net.Listener, error) {
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}
	if cfg.Insecure {
		return ln, nil
	}

	var tlsCfg *tls.Config
	if reloader != nil {
		tlsCfg = reloader.TLSConfig()
	} else {
		tlsCfg, err = certs.BuildServerTLSConfig(cfg.CertFile, cfg.KeyFile, cfg.CAFile, cfg.CRLFile)
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("build TLS config: %w", err)
		}
	}
	return tls.NewListener(ln, tlsCfg), nil
}

func runInitCA(args []string) error {
	fs := flag.NewFlagSet("init-ca", flag.ContinueOnError)
	out := fs.String("out", ".", "output directory for CA and server certificates")
	days := fs.Int("days", certs.DefaultCADays, "CA validity in days")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := certs.InitCA(certs.InitCAOptions{Dir: *out, Days: *days}); err != nil {
		return err
	}
	fmt.Printf("CA and server certificates written to %s\n", *out)
	fmt.Printf("  %s  (CA certificate)\n", filepath.Join(*out, "ca.crt"))
	fmt.Printf("  %s  (CA private key)\n", filepath.Join(*out, "ca.key"))
	fmt.Printf("  %s  (server certificate)\n", filepath.Join(*out, "server.crt"))
	fmt.Printf("  %s  (server private key)\n", filepath.Join(*out, "server.key"))
	return nil
}

func runIssueCert(args []string) error {
	fs := flag.NewFlagSet("issue-cert", flag.ContinueOnError)
	name := fs.String("name", "", "certificate CommonName / filename prefix (required)")
	caDir := fs.String("ca-dir", ".", "directory containing ca.crt and ca.key")
	out := fs.String("out", "", "output directory for new certificate (defaults to --ca-dir)")
	days := fs.Int("days", certs.DefaultCertDays, "certificate validity in days")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("issue-cert: --name is required")
	}
	outDir := *out
	if outDir == "" {
		outDir = *caDir
	}
	if err := certs.IssueClientCert(certs.IssueOptions{
		CADir:  *caDir,
		OutDir: outDir,
		Name:   *name,
		Days:   *days,
	}); err != nil {
		return err
	}
	fmt.Printf("Client certificate issued: %s/%s.crt\n", outDir, *name)
	return nil
}

func runListCerts(args []string) (string, error) {
	fs := flag.NewFlagSet("list-certs", flag.ContinueOnError)
	caDir := fs.String("ca-dir", ".", "directory to list certificates from")
	if err := fs.Parse(args); err != nil {
		return "", err
	}

	crlPath := filepath.Join(*caDir, "crl.pem")
	revoked, err := certs.LoadRevokedSerials(crlPath)
	if err != nil {
		return "", fmt.Errorf("list-certs: load CRL: %w", err)
	}

	infos, err := certs.ListCertificates(*caDir, revoked)
	if err != nil {
		return "", fmt.Errorf("list-certs: %w", err)
	}

	if len(infos) == 0 {
		return "No certificates found.\n", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-20s  %-20s  %-12s  %s\n", "NAME", "SERIAL", "EXPIRES", "STATUS"))
	sb.WriteString(strings.Repeat("-", 80) + "\n")
	for _, info := range infos {
		status := "valid"
		if info.Revoked {
			status = "REVOKED"
		}
		name := info.CommonName
		if name == "" {
			name = filepath.Base(info.Path)
		}
		sb.WriteString(fmt.Sprintf("%-20s  %-20s  %-12s  %s\n",
			name,
			info.SerialNumber[:min(20, len(info.SerialNumber))],
			info.NotAfter.Format("2006-01-02"),
			status,
		))
	}
	return sb.String(), nil
}

func runRevokeCert(args []string) error {
	fs := flag.NewFlagSet("revoke-cert", flag.ContinueOnError)
	name := fs.String("name", "", "certificate name to revoke (required)")
	caDir := fs.String("ca-dir", ".", "directory containing the certificate and crl.pem")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("revoke-cert: --name is required")
	}
	if err := certs.RevokeCertificate(*caDir, *name); err != nil {
		return err
	}
	fmt.Printf("Certificate %q revoked; updated %s/crl.pem\n", *name, *caDir)
	return nil
}

