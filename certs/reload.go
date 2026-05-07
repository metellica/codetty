package certs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"sync"
)

// CertReloader provides hot-reloadable TLS configuration for the hub server.
// It caches the server certificate and CA pool, reloading them on demand via
// the Reload method (typically triggered by SIGHUP).
//
// The CRL is already reloaded per-handshake by BuildServerTLSConfig's
// VerifyPeerCertificate callback, so it does not need caching here.
type CertReloader struct {
	certFile string
	keyFile  string
	caFile   string
	crlFile  string

	mu     sync.RWMutex
	cert   *tls.Certificate
	caPool *x509.CertPool
}

// NewCertReloader creates a CertReloader and performs the initial load of all
// TLS material. Returns an error if the initial load fails.
func NewCertReloader(certFile, keyFile, caFile, crlFile string) (*CertReloader, error) {
	r := &CertReloader{
		certFile: certFile,
		keyFile:  keyFile,
		caFile:   caFile,
		crlFile:  crlFile,
	}
	if err := r.Reload(); err != nil {
		return nil, fmt.Errorf("initial TLS load: %w", err)
	}
	return r, nil
}

// Reload re-reads the server certificate and CA pool from disk. It is safe to
// call from any goroutine. On success the cached material is atomically
// swapped; on failure the previous material remains in effect and an error is
// returned.
func (r *CertReloader) Reload() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("certs: reload server keypair: %w", err)
	}

	caPool, err := loadCAPool(r.caFile)
	if err != nil {
		return fmt.Errorf("certs: reload CA pool: %w", err)
	}

	r.mu.Lock()
	r.cert = &cert
	r.caPool = caPool
	r.mu.Unlock()

	return nil
}

// TLSConfig builds a *tls.Config that uses the reloader's cached material.
// The returned config uses GetCertificate and GetConfigForClient callbacks so
// that a successful Reload() is reflected on subsequent TLS handshakes without
// needing to replace the listener.
func (r *CertReloader) TLSConfig() *tls.Config {
	crlFile := r.crlFile

	return &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			r.mu.RLock()
			defer r.mu.RUnlock()
			return r.cert, nil
		},
		ClientAuth: tls.RequireAndVerifyClientCert,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			r.mu.RLock()
			pool := r.caPool
			r.mu.RUnlock()

			return &tls.Config{
				GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
					r.mu.RLock()
					defer r.mu.RUnlock()
					return r.cert, nil
				},
				ClientAuth: tls.RequireAndVerifyClientCert,
				ClientCAs:  pool,
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					if len(rawCerts) == 0 {
						return nil
					}
					revoked, err := LoadRevokedSerials(crlFile)
					if err != nil {
						return fmt.Errorf("certs: read CRL: %w", err)
					}
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
			}, nil
		},
	}
}

// LogStatus logs the currently loaded certificate details for operational
// visibility (typically called after a successful reload).
func (r *CertReloader) LogStatus() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.cert == nil {
		log.Printf("[tls-reload] no certificate loaded")
		return
	}
	if r.cert.Leaf == nil {
		// Parse the leaf for logging if not already cached.
		if len(r.cert.Certificate) > 0 {
			leaf, err := x509.ParseCertificate(r.cert.Certificate[0])
			if err == nil {
				log.Printf("[tls-reload] cert CN=%s expires=%s", leaf.Subject.CommonName, leaf.NotAfter.Format("2006-01-02"))
				return
			}
		}
	} else {
		log.Printf("[tls-reload] cert CN=%s expires=%s", r.cert.Leaf.Subject.CommonName, r.cert.Leaf.NotAfter.Format("2006-01-02"))
		return
	}
	log.Printf("[tls-reload] certificate loaded (details unavailable)")
}
