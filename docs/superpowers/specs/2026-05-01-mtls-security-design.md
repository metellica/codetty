# CodeTTY mTLS Security Design

## Problem

CodeTTY's current client/server architecture exposes terminal sessions over plain WebSocket (`ws://`) with no authentication, encryption, or access control. Any network-reachable client can connect to the hub, view terminal output, and send input—a significant security risk even in LAN/intranet environments.

## Approach

Implement mutual TLS (mTLS) as the default security model, with a built-in certificate management toolchain to lower operational friction. Provide an explicit `--insecure` escape hatch for development/testing that preserves the current open behavior.

## Design

### §1 Security Modes

CodeTTY operates in one of two modes:

**Secure mode (default — mTLS).** Hub requires `--cert`, `--key`, and `--ca` flags (or equivalent config). CLI requires `--cert`, `--key`, and `--ca`. TLS handshake enforces mutual certificate verification. Hub refuses to start without certificates and prints a helpful error message suggesting `init-ca` or `--insecure`.

```bash
# Hub
codetty-hub --cert server.crt --key server.key --ca ca.crt

# CLI
codetty-cli --cert alice.crt --key alice.key --ca ca.crt \
  --server wss://hub:8080/ws -- bash
```

**Insecure mode (`--insecure`).** Both hub and CLI accept the `--insecure` flag. When set, TLS is disabled and no authentication is performed—identical to the current behavior. A prominent warning is printed at startup:

```
⚠️  WARNING: Running in INSECURE mode. No authentication or encryption.
⚠️  Anyone on the network can connect and control terminal sessions.
⚠️  This is intended for development/testing only.
```

Both sides must agree: a secure hub rejects non-TLS connections; an insecure hub does not attempt TLS.

### §2 Certificate Management Toolchain

Built-in subcommands on `codetty-hub` to manage a lightweight local CA.

**Subcommands:**

| Command | Purpose |
|---------|---------|
| `codetty-hub init-ca [--out <dir>] [--days <n>]` | Create CA root cert + hub server cert. Default output: `~/.codetty/certs/`. Default CA validity: 10 years. Default server cert validity: 1 year. |
| `codetty-hub issue-cert --name <user> [--out <dir>] [--ca-dir <dir>] [--days <n>]` | Sign a client certificate for `<user>`. CN is set to the user name. Default validity: 1 year. |
| `codetty-hub list-certs [--ca-dir <dir>]` | List issued certificates by scanning `<ca-dir>` for `.crt` files, parsing each to show name (CN), serial number, expiry date, and revocation status (cross-referenced with CRL). |
| `codetty-hub revoke-cert --name <user> [--ca-dir <dir>]` | Add certificate to CRL (`crl.pem`). Hub hot-reloads CRL. |

**Certificate details:**

- Algorithm: Ed25519
- CA validity: 10 years (default)
- Server/client certificate validity: 1 year (default), adjustable via `--days`
- Client certificate CN: user name from `--name`
- CRL: PEM file at `<ca-dir>/crl.pem`, hub hot-reloads on change

**Default paths:**

| Role | Default cert directory |
|------|----------------------|
| Hub (CA + server) | `~/.codetty/certs/` |
| CLI (client) | No default; specify via `--cert`, `--key`, `--ca` or `--cert-dir` |

**File permissions:** All `.key` files are created with mode `0600`.

**Typical workflow:**

```bash
# Admin initializes CA and issues certs
codetty-hub init-ca
codetty-hub issue-cert --name alice
codetty-hub issue-cert --name bob

# Distribute ca.crt + personal .crt/.key via secure channel

# Alice connects
codetty-cli --cert alice.crt --key alice.key --ca ca.crt \
  --server wss://hub-host:8080/ws -- bash

# Admin revokes Bob's access
codetty-hub revoke-cert --name bob
# Hub hot-reloads CRL; Bob's subsequent connections are rejected
```

### §3 Connection Handshake & Protocol Changes

**TLS layer.**

In secure mode, hub uses `tls.Listen` + `http.Serve` instead of `http.ListenAndServe`. TLS config:

- Hub: `ClientAuth: tls.RequireAndVerifyClientCert`, `ClientCAs` loaded from CA cert. CRL is checked via Go's `VerifyPeerCertificate` callback—this runs after the TLS handshake verifies the certificate chain but before the connection is made available to the application. From the client's perspective, a revoked cert results in a closed connection.
- CLI: `RootCAs` loaded from CA cert, `Certificates` loaded from client cert + key.

In insecure mode, hub uses plain `http.ListenAndServe` as today.

**Protocol layer.**

`RegisterPayload` gains a `User` field:

```go
type RegisterPayload struct {
    ID    string `json:"id"`
    Title string `json:"title"`
    Cols  int    `json:"cols"`
    Rows  int    `json:"rows"`
    User  string `json:"user,omitempty"`
}
```

In secure mode, hub extracts the user name from `r.TLS.PeerCertificates[0].Subject.CommonName` after WebSocket upgrade and verifies it matches the `User` field in the register message. Mismatch causes connection close.

In insecure mode, `User` is empty or "anonymous"; no verification is performed.

**Connection rejection scenarios:**

| Scenario | Behavior |
|----------|----------|
| No client certificate | TLS handshake failure |
| Certificate signed by untrusted CA | TLS handshake failure |
| Certificate revoked (in CRL) | Connection closed during `VerifyPeerCertificate` callback |
| Certificate expired | TLS handshake failure |
| Register `User` ≠ certificate CN | Hub closes WebSocket |

### §4 Code Architecture

**New package:**

```
certs/
├── ca.go           # CA init, cert issuance, revocation, CRL generation
├── loader.go       # Cert loading, CRL parsing, TLS config builders
└── certs_test.go   # Unit tests
```

**New file:**

```
cmd/codetty-hub/certcmd.go   # init-ca, issue-cert, list-certs, revoke-cert subcommands
```

**Modified files:**

| File | Change |
|------|--------|
| `cmd/codetty-hub/main.go` | Add `--cert`, `--key`, `--ca`, `--insecure` flags. Route subcommands. Start TLS or plain server. |
| `cmd/codetty-cli/main.go` | Add `--cert`, `--key`, `--ca`, `--insecure` flags. Build TLS dialer config. |
| `hub/hub.go` | `HandleConn` accepts user identity parameter. Validates `User` vs cert CN in secure mode. |
| `hub/tui.go` | Status bar displays connected user name. |
| `protocol/protocol.go` | `RegisterPayload` adds `User` field with `omitempty`. |

**Design principles:**

1. `certs` package is independent of hub/cli — pure certificate operations with no WebSocket or TUI dependencies.
2. TLS config is built at the entry point (`main.go`) and passed into HTTP server / WebSocket dialer.
3. Hub core logic does not know TLS details — `HandleConn` only cares about "what is this connection's user name", received as a parameter.
4. Insecure mode is a code path, not a separate code base — `if insecure { skip TLS setup }`, no duplication.

### §5 Testing Strategy

| Layer | What | Where |
|-------|------|-------|
| Unit | CA creation, cert issuance, CRL generation/parsing | `certs/certs_test.go` |
| Unit | TLS config construction (server/client) | `certs/loader_test.go` |
| Integration | mTLS handshake success/failure (no cert, expired, revoked) | `hub/hub_test.go` |
| Integration | Insecure mode connection | `hub/hub_test.go` |
| Protocol | RegisterPayload new field compatibility | `protocol/protocol_test.go` |
| Integration | User field vs cert CN consistency | `hub/hub_test.go` |

**Key test scenarios:**

- ✅ Valid cert → connection succeeds, user identity correct
- ❌ No cert → TLS handshake fails
- ❌ Wrong CA cert → TLS handshake fails
- ❌ Revoked cert → connection rejected
- ❌ Register.User ≠ cert CN → connection closed
- ✅ `--insecure` mode → current behavior preserved

### §6 Backward Compatibility

- **Protocol compatible:** `RegisterPayload.User` is `omitempty`; old CLI messages parse without error (User is empty string).
- **Insecure mode = current behavior:** `--insecure` fully preserves existing no-auth no-encryption behavior for zero migration cost.
- **Default port unchanged:** Still `:8080`, only the protocol changes from `ws://` to `wss://`.

### §7 Security Considerations

- **CA key protection:** `ca.key` file permissions set to `0600`, only hub admin can read.
- **CRL hot-reload:** Hub periodically re-reads the CRL file (default: every 30 seconds) to apply revocations without restart. This avoids adding `fsnotify` as a dependency.
- **Audit logging:** Connection/disconnection events log user name, certificate fingerprint, and timestamp.
