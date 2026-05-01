package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"codetty/certs"
)

// runInitCA handles the `init-ca` subcommand.
// Creates a CA and initial server certificate in --out dir.
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

// runIssueCert handles the `issue-cert` subcommand.
// Issues a new client certificate signed by the CA in --ca-dir.
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

// runListCerts handles the `list-certs` subcommand.
// Returns formatted output listing certificates in --ca-dir.
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

// runRevokeCert handles the `revoke-cert` subcommand.
// Adds the named certificate to the CRL in --ca-dir.
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
