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
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"

	"codetty/certs"
	"codetty/hub"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// hubConfig holds parsed configuration for the hub server.
type hubConfig struct {
	Addr     string
	CertFile string
	KeyFile  string
	CAFile   string
	CRLFile  string
	Insecure bool
}

// parseHubFlags parses hub server flags from args.
// Returns an error if secure mode is requested but cert/key/ca are missing.
func parseHubFlags(args []string) (hubConfig, error) {
	fs := flag.NewFlagSet("codetty-hub", flag.ContinueOnError)
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
				"codetty-hub requires --cert, --key, and --ca for secure (mTLS) mode\n" +
					"  Generate certificates with: codetty-hub init-ca\n" +
					"  Or start without mTLS using: codetty-hub --insecure",
			)
		}
	}
	return cfg, nil
}

// buildHubListener creates a TCP listener, wrapping it with TLS when in secure mode.
// When a CertReloader is provided, it uses the reloader's dynamic TLS config;
// otherwise it falls back to the static BuildServerTLSConfig.
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

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init-ca":
			if err := runInitCA(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "issue-cert":
			if err := runIssueCert(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "list-certs":
			out, err := runListCerts(os.Args[2:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(out)
			return
		case "revoke-cert":
			if err := runRevokeCert(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	cfg, err := parseHubFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if cfg.Insecure {
		log.Printf("WARNING: running in insecure ws:// mode — mTLS is disabled")
		log.Printf("WARNING: all connections are unauthenticated; use only in trusted environments")
	}

	// Initialize TLS cert reloader (nil in insecure mode).
	var reloader *certs.CertReloader
	if !cfg.Insecure {
		var err error
		reloader, err = certs.NewCertReloader(cfg.CertFile, cfg.KeyFile, cfg.CAFile, cfg.CRLFile)
		if err != nil {
			log.Fatalf("tls init: %v", err)
		}
		reloader.LogStatus()
	}

	h := hub.New()
	model := hub.NewModel(h)

	// Start TUI
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
		log.Printf("codetty-hub listening on %s://%s/ws", scheme, cfg.Addr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	// Signal handling: SIGHUP → reload TLS, SIGTERM/SIGINT → graceful shutdown.
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
				// Graceful HTTP shutdown: stop accepting new connections,
				// close idle connections, wait for active ones to finish.
				if err := srv.Shutdown(context.Background()); err != nil {
					log.Printf("[signal] HTTP shutdown error: %v", err)
				}
				// Quit the TUI program which will unblock p.Run() below.
				p.Quit()
				return
			}
		}
	}()

	// Run TUI (blocks until quit)
	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}
