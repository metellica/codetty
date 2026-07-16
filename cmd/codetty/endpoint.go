package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"codetty/config"
)

type daemonEndpoint interface {
	ID() string
	Ensure() error
	Dial() (io.ReadWriteCloser, error)
}

type localDaemonEndpoint struct {
	sockPath string
}

func (e localDaemonEndpoint) ID() string { return "local" }

func (e localDaemonEndpoint) Ensure() error {
	return ensureDaemon(e.sockPath)
}

func (e localDaemonEndpoint) Dial() (io.ReadWriteCloser, error) {
	return net.Dial("unix", e.sockPath)
}

type sshDaemonEndpoint struct {
	id       string
	name     string
	host     string
	instance string
	deploy   string
	binary   string
	// preinstalled is set by Ensure when auto-deploy cannot run (e.g. a
	// Windows client and a Linux remote) but a previously deployed remote
	// binary exists: bridge/upgrade then use the stable symlink path.
	preinstalled bool
}

func newSSHDaemonEndpoint(name string, def config.RemoteDef) (*sshDaemonEndpoint, error) {
	if strings.TrimSpace(def.Host) != def.Host || def.Host == "" || strings.HasPrefix(def.Host, "-") {
		return nil, fmt.Errorf("remote %q: invalid SSH host %q", name, def.Host)
	}
	for _, r := range def.Host {
		if r <= ' ' || r == 0x7f {
			return nil, fmt.Errorf("remote %q: invalid SSH host %q", name, def.Host)
		}
	}

	instance := def.Instance
	if instance == "" {
		instance = "default"
	}
	if err := validateInstanceName(instance); err != nil {
		return nil, fmt.Errorf("remote %q: %w", name, err)
	}

	deploy := def.Deploy
	if deploy == "" {
		deploy = "auto"
	}
	binary := def.Binary
	if deploy == "auto" {
		binary = "$HOME/.local/lib/codetty/codetty"
	} else if binary == "" {
		binary = "codetty"
	}
	if strings.ContainsAny(binary, "\r\n\x00") {
		return nil, fmt.Errorf("remote %q: invalid binary path", name)
	}

	return &sshDaemonEndpoint{
		id:       remoteEndpointID(def.Host, instance),
		name:     name,
		host:     def.Host,
		instance: instance,
		deploy:   deploy,
		binary:   binary,
	}, nil
}

func (e *sshDaemonEndpoint) ID() string { return e.id }

func remoteEndpointID(host, instance string) string {
	sum := sha256.Sum256([]byte(host + "\x00" + instance))
	return "remote:" + hex.EncodeToString(sum[:6])
}

func (e *sshDaemonEndpoint) Ensure() error {
	if _, err := exec.LookPath("ssh"); err != nil {
		return fmt.Errorf("remote %q requires the OpenSSH client: %w", e.name, err)
	}
	if e.deploy == "never" {
		command := fmt.Sprintf(
			"command -v %s >/dev/null 2>&1 || test -x %s",
			shellQuote(e.binary),
			shellQuote(e.binary),
		)
		if _, err := e.runSSH(30*time.Second, nil, command); err != nil {
			return fmt.Errorf("remote %q: CodeTTY binary %q is unavailable: %w", e.name, e.binary, err)
		}
		return nil
	}

	// Content-addressed deploy: the binary installs under a name derived
	// from its own hash, so concurrent clients running different local
	// versions never overwrite each other's deployed binary — each
	// bridge/upgrade references its exact version path.
	hash, err := localBinaryHash()
	if err != nil {
		return fmt.Errorf("remote %q: hash local CodeTTY binary: %w", e.name, err)
	}
	check := fmt.Sprintf(`test -x "$HOME/.local/lib/codetty/versions/codetty-%s"`, hash)
	if _, err := e.runSSH(30*time.Second, nil, check); err == nil {
		return nil
	}

	if err := e.checkMachine(); err != nil {
		// The local binary cannot run on the remote platform (e.g. a
		// Windows client and a Linux remote). Fall back to a binary
		// previously deployed by a matching-platform client.
		symlink := `test -x "$HOME/.local/lib/codetty/codetty"`
		if _, symErr := e.runSSH(30*time.Second, nil, symlink); symErr == nil {
			e.preinstalled = true
			return nil
		}
		return fmt.Errorf("%w (and no previously deployed binary found; run `codetty upgrade -r %s` from a matching host or set deploy = \"never\" with a binary path)", err, e.name)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("remote %q: locate local CodeTTY binary: %w", e.name, err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return fmt.Errorf("remote %q: read local CodeTTY binary: %w", e.name, err)
	}

	// Install the exact version, refresh the convenience symlink, and
	// garbage-collect all but the most recent versions (keeping a few so
	// other hosts' concurrently-used versions aren't yanked from under
	// their next bridge exec).
	install := fmt.Sprintf(`
set -eu
dir="$HOME/.local/lib/codetty"
vdir="$dir/versions"
path="$vdir/codetty-%[1]s"
tmp="$vdir/.codetty.tmp.$$"
mkdir -p "$vdir"
trap 'rm -f "$tmp"' EXIT HUP INT TERM
cat >"$tmp"
chmod 700 "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
ln -sfn "$path" "$dir/codetty"
ls -1t "$vdir" 2>/dev/null | grep '^codetty-' | tail -n +4 | while read -r old; do
  [ "$old" = "codetty-%[1]s" ] || rm -f "$vdir/$old"
done`, hash)
	if _, err := e.runSSH(5*time.Minute, bytes.NewReader(data), install); err != nil {
		return fmt.Errorf("remote %q: deploy CodeTTY: %w", e.name, err)
	}
	return nil
}

// localBinaryHash returns a short content hash of the running executable,
// used as the content-addressed deploy name (computed once).
var (
	localBinaryHashOnce sync.Once
	localBinaryHashVal  string
	localBinaryHashErr  error
)

func localBinaryHash() (string, error) {
	localBinaryHashOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			localBinaryHashErr = err
			return
		}
		f, err := os.Open(exe)
		if err != nil {
			localBinaryHashErr = err
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			localBinaryHashErr = err
			return
		}
		localBinaryHashVal = hex.EncodeToString(h.Sum(nil))[:12]
	})
	return localBinaryHashVal, localBinaryHashErr
}

func (e *sshDaemonEndpoint) checkMachine() error {
	output, err := e.runSSH(30*time.Second, nil, "uname -s; uname -m")
	if err != nil {
		return fmt.Errorf("remote %q: detect platform: %w", e.name, err)
	}
	lines := strings.Fields(string(output))
	if len(lines) < 2 {
		return fmt.Errorf("remote %q: unexpected platform response %q", e.name, strings.TrimSpace(string(output)))
	}

	remoteOS := strings.ToLower(lines[0])
	if remoteOS == "darwin" {
		remoteOS = "darwin"
	}
	remoteArch := strings.ToLower(lines[1])
	switch remoteArch {
	case "x86_64":
		remoteArch = "amd64"
	case "aarch64":
		remoteArch = "arm64"
	}
	if remoteOS != runtime.GOOS || remoteArch != runtime.GOARCH {
		return fmt.Errorf(
			"remote %q: auto-deploy requires matching platforms (local %s/%s, remote %s/%s)",
			e.name,
			runtime.GOOS,
			runtime.GOARCH,
			remoteOS,
			remoteArch,
		)
	}
	return nil
}

func (e *sshDaemonEndpoint) Dial() (io.ReadWriteCloser, error) {
	cmd := exec.Command("ssh", "-T", e.host, e.bridgeCommand())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &sshProcessConn{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func (e *sshDaemonEndpoint) bridgeCommand() string {
	return fmt.Sprintf("exec %s -i %s __bridge", e.remoteBinary(), shellQuote(e.instance))
}

// remoteBinary returns the shell-quoted remote binary path. Auto-deployed
// binaries are addressed by their exact content hash so concurrent clients
// with different versions each exec the binary they deployed; the stable
// symlink is used when this client couldn't deploy itself (cross-platform
// client relying on a preinstalled binary) or the local hash is unknown.
func (e *sshDaemonEndpoint) remoteBinary() string {
	if e.deploy == "auto" {
		if hash, err := localBinaryHash(); err == nil && !e.preinstalled {
			return fmt.Sprintf(`"$HOME/.local/lib/codetty/versions/codetty-%s"`, hash)
		}
		return `"$HOME/.local/lib/codetty/codetty"`
	}
	return shellQuote(e.binary)
}

// UpgradeDaemon deploys the local binary if needed (per the deploy policy,
// via Ensure) and hot-upgrades the remote daemon in place. Runs in the same
// SSH environment as the bridge, so the daemon socket resolves identically.
func (e *sshDaemonEndpoint) UpgradeDaemon() (string, error) {
	if err := e.Ensure(); err != nil {
		return "", err
	}
	binary := e.remoteBinary()
	script := fmt.Sprintf("%s -i %s upgrade && %s --version",
		binary, shellQuote(e.instance), binary)
	output, err := e.runSSH(60*time.Second, nil, script)
	return strings.TrimSpace(string(output)), err
}

func (e *sshDaemonEndpoint) runSSH(timeout time.Duration, stdin io.Reader, script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh", e.host, "sh -c "+shellQuote(script))
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.Bytes(), fmt.Errorf("SSH command timed out")
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, message)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type sshProcessConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	once   sync.Once
}

func (c *sshProcessConn) Read(p []byte) (int, error) {
	return c.stdout.Read(p)
}

func (c *sshProcessConn) Write(p []byte) (int, error) {
	return c.stdin.Write(p)
}

func (c *sshProcessConn) Close() error {
	var closeErr error
	c.once.Do(func() {
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		if c.cmd.Process != nil {
			closeErr = c.cmd.Process.Kill()
		}
		_ = c.cmd.Wait()
	})
	return closeErr
}
