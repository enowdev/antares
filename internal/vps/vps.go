// Package vps connects to a user's server over SSH and reads its state on
// demand — no agent installed on the box, just standard commands whose output
// is parsed into metrics. It also runs arbitrary commands and transfers files
// for the VPS-manager tools.
package vps

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Target is everything needed to dial one host.
type Target struct {
	Host       string
	Port       int
	Username   string
	AuthMethod string // password|key
	Password   string
	PrivateKey string
	Passphrase string
	// KnownHostKey is the server's pinned SSH public key (authorized_keys format,
	// e.g. "ssh-ed25519 AAAA..."). Empty means first use: any key is accepted and
	// returned via the connection's SeenHostKey for the caller to pin. Non-empty
	// means the presented key MUST match, or the dial fails — this is what stops
	// a MITM from harvesting the credentials.
	KnownHostKey string
}

func (t Target) addr() string {
	p := t.Port
	if p == 0 {
		p = 22
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(p))
}

// ErrHostKeyChanged is returned when a host presents a key different from the
// pinned one — a possible man-in-the-middle, or a legitimately rebuilt server.
var ErrHostKeyChanged = errors.New("host key changed since it was first trusted — possible MITM, or the server was rebuilt; remove and re-add it if you trust the change")

// ErrTimeout is returned when a remote command or transfer exceeds its deadline.
// The error text includes the duration; partial output may accompany it from Run.
var ErrTimeout = errors.New("vps operation timed out")

// conn wraps an ssh.Client with the host key the server actually presented, so
// the caller can pin it after a first-use connect.
type conn struct {
	*ssh.Client
	seenHostKey string
}

// dial opens an SSH client with trust-on-first-use host-key verification. On
// first use (t.KnownHostKey empty) it records the key; thereafter it must match.
func dial(ctx context.Context, t Target) (*conn, error) {
	auth, err := authMethods(t)
	if err != nil {
		return nil, err
	}
	user := t.Username
	if user == "" {
		user = "root"
	}

	var seen string
	hostKeyCb := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		seen = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if known := strings.TrimSpace(t.KnownHostKey); known != "" && !hostKeysEqual(known, seen) {
			return ErrHostKeyChanged
		}
		return nil
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: hostKeyCb,
		// Handshake timeout only. Command runtime is bounded by the caller's ctx.
		Timeout: 20 * time.Second,
	}
	// Dial timeout is separate from the overall command timeout so a slow host
	// does not burn the whole vps_run budget before the command starts.
	d := net.Dialer{Timeout: 20 * time.Second}
	netConn, err := d.DialContext(ctx, "tcp", t.addr())
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w while connecting to %s: %v", ErrTimeout, t.addr(), err)
		}
		return nil, fmt.Errorf("connect %s: %w", t.addr(), err)
	}
	// Honour cancellation during the SSH handshake too.
	if deadline, ok := ctx.Deadline(); ok {
		_ = netConn.SetDeadline(deadline)
	}
	c, chans, reqs, err := ssh.NewClientConn(netConn, t.addr(), cfg)
	if err != nil {
		netConn.Close()
		if errors.Is(err, ErrHostKeyChanged) {
			return nil, ErrHostKeyChanged
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w during ssh handshake with %s: %v", ErrTimeout, t.addr(), err)
		}
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	// Clear the dial deadline so long-running commands are not cut off by it.
	_ = netConn.SetDeadline(time.Time{})
	client := ssh.NewClient(c, chans, reqs)
	// Keep the TCP session alive through NAT/firewalls during long systemctl
	// restarts and package upgrades. Best-effort; ignored if the server does not
	// recognise the request.
	go keepAlive(ctx, client)
	return &conn{Client: client, seenHostKey: seen}, nil
}

func keepAlive(ctx context.Context, client *ssh.Client) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				return
			}
		}
	}
}

// hostKeysEqual compares two authorized_keys lines by their type+base64 body,
// ignoring any trailing comment.
func hostKeysEqual(a, b string) bool {
	fa, fb := strings.Fields(a), strings.Fields(b)
	if len(fa) < 2 || len(fb) < 2 {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return fa[0] == fb[0] && fa[1] == fb[1]
}

func authMethods(t Target) ([]ssh.AuthMethod, error) {
	if t.AuthMethod == "key" || (t.AuthMethod == "" && t.PrivateKey != "") {
		key := strings.TrimSpace(t.PrivateKey)
		if key == "" {
			return nil, fmt.Errorf("auth method is key but no private key was provided")
		}
		var signer ssh.Signer
		var err error
		if t.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(key), []byte(t.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(key))
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	if t.Password == "" {
		return nil, fmt.Errorf("no password or private key configured")
	}
	// Many cloud images and hardened OpenSSH configs offer only
	// keyboard-interactive (or prefer it over "password"). Offering both
	// methods is what OpenSSH clients do and is required for those hosts.
	return []ssh.AuthMethod{
		ssh.Password(t.Password),
		ssh.KeyboardInteractive(passwordKeyboardInteractive(t.Password)),
	}, nil
}

// passwordKeyboardInteractive answers every prompt with the stored password.
// Servers that ask a single "Password:" question work; multi-factor prompts
// that need a second factor will still fail (as they should without the factor).
func passwordKeyboardInteractive(password string) ssh.KeyboardInteractiveChallenge {
	return func(_, _ string, questions []string, _ []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range questions {
			answers[i] = password
		}
		return answers, nil
	}
}

// Run opens a connection, runs one command, and returns its combined output
// plus the host key the server presented (for TOFU pinning). A non-zero exit is
// returned with the output so the caller sees stderr rather than a bare error.
func Run(ctx context.Context, t Target, command string) (string, string, error) {
	client, err := dial(ctx, t)
	if err != nil {
		return "", "", err
	}
	defer client.Close()
	out, err := runOn(ctx, client.Client, command)
	return out, client.seenHostKey, err
}

func runOn(ctx context.Context, client *ssh.Client, command string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	// Closing the session is the reliable way to unblock CombinedOutput on
	// cancel; Signal alone is frequently ignored without a PTY.
	defer sess.Close()

	// Best-effort: keep remote tools from waiting on a pager. Setenv is often
	// refused by the server (AcceptEnv); failures are ignored.
	_ = sess.Setenv("SYSTEMD_PAGER", "cat")
	_ = sess.Setenv("PAGER", "cat")
	_ = sess.Setenv("SYSTEMD_COLORS", "0")
	_ = sess.Setenv("GIT_PAGER", "cat")

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(command)
		done <- result{out: out, err: err}
	}()

	select {
	case <-ctx.Done():
		// Signal first (best-effort), then close so CombinedOutput returns.
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		res := <-done // wait so there is no race on the output buffer
		partial := string(res.out)
		cause := ctx.Err()
		if cause == nil {
			cause = ErrTimeout
		}
		return partial, fmt.Errorf("%w: %v", ErrTimeout, cause)
	case res := <-done:
		return string(res.out), res.err
	}
}

// Process is one row from the remote process table.
type Process struct {
	PID     int     `json:"pid"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`
	Command string  `json:"command"`
}

// Processes lists the running processes on a host, busiest CPU first, plus the
// host key seen. Runs a single ps over one connection.
func Processes(ctx context.Context, t Target) ([]Process, string, error) {
	out, seen, err := Run(ctx, t, `ps -eo pid,user,pcpu,pmem,comm --sort=-pcpu --no-headers 2>/dev/null | head -n 300`)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, seen, err
	}
	var procs []Process
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 5 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		cpu, _ := strconv.ParseFloat(f[2], 64)
		mem, _ := strconv.ParseFloat(f[3], 64)
		procs = append(procs, Process{
			PID: pid, User: f[1], CPU: cpu, Mem: mem,
			Command: strings.Join(f[4:], " "),
		})
	}
	return procs, seen, nil
}

// Ping confirms a host is reachable and authenticates, returning the remote
// user@hostname it landed on (proof it really connected) plus the host key seen.
func Ping(ctx context.Context, t Target) (who string, seenHostKey string, err error) {
	client, err := dial(ctx, t)
	if err != nil {
		return "", "", err
	}
	defer client.Close()
	out, err := runOn(ctx, client.Client, "whoami; hostname")
	if err != nil {
		return "", client.seenHostKey, err
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) >= 2 {
		return fields[0] + "@" + fields[1], client.seenHostKey, nil
	}
	return strings.TrimSpace(out), client.seenHostKey, nil
}
