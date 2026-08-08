package vps

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// testSSHServer is a minimal password-auth SSH server on loopback that either
// runs shell commands via "exec" requests or serves SFTP on "subsystem sftp".
type testSSHServer struct {
	addr     string
	listener net.Listener
	config   *ssh.ServerConfig
	wg       sync.WaitGroup
	closed   chan struct{}

	// delayExec, when set, sleeps before responding to exec (simulates slow systemctl).
	delayExec time.Duration
	// execOutput is returned as stdout for any exec command.
	execOutput string
	// requireKbdInt forces keyboard-interactive instead of password.
	requireKbdInt bool
	// sftpHandlers is shared across sessions so upload then download sees the same fs.
	sftpHandlers sftp.Handlers
}

func newTestSSHServer(t *testing.T, opts ...func(*testSSHServer)) *testSSHServer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	s := &testSSHServer{
		closed:       make(chan struct{}),
		execOutput:   "ok\n",
		sftpHandlers: sftp.InMemHandler(),
	}
	for _, o := range opts {
		o(s)
	}

	cfg := &ssh.ServerConfig{}
	if s.requireKbdInt {
		cfg.KeyboardInteractiveCallback = func(c ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			ans, err := challenge("user", "", []string{"Password: "}, []bool{false})
			if err != nil {
				return nil, err
			}
			if len(ans) == 1 && ans[0] == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("bad password")
		}
	} else {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("bad password")
		}
	}
	cfg.AddHostKey(signer)
	s.config = cfg

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.listener = ln
	s.addr = ln.Addr().String()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-s.closed:
					return
				default:
					return
				}
			}
			s.wg.Add(1)
			go func(nc net.Conn) {
				defer s.wg.Done()
				s.handleConn(nc)
			}(conn)
		}
	}()

	t.Cleanup(func() {
		close(s.closed)
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *testSSHServer) handleConn(nc net.Conn) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, s.config)
	if err != nil {
		_ = nc.Close()
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, requests, err := newCh.Accept()
		if err != nil {
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleSession(ch, requests)
		}()
	}
}

func (s *testSSHServer) handleSession(ch ssh.Channel, requests <-chan *ssh.Request) {
	defer ch.Close()
	for req := range requests {
		switch req.Type {
		case "env":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		case "exec":
			if s.delayExec > 0 {
				time.Sleep(s.delayExec)
			}
			_, _ = ch.Write([]byte(s.execOutput))
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
			return
		case "subsystem":
			name := ""
			if len(req.Payload) >= 4 {
				l := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
				if l > 0 && 4+l <= len(req.Payload) {
					name = string(req.Payload[4 : 4+l])
				}
			}
			if name == "sftp" {
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
				server := sftp.NewRequestServer(ch, s.sftpHandlers)
				_ = server.Serve()
				_ = server.Close()
				return
			}
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			return
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

func (s *testSSHServer) target() Target {
	host, portStr, _ := net.SplitHostPort(s.addr)
	port := 22
	fmt.Sscanf(portStr, "%d", &port)
	return Target{
		Host: host, Port: port, Username: "test",
		AuthMethod: "password", Password: "secret",
	}
}

func TestAuthMethodsIncludesKeyboardInteractive(t *testing.T) {
	methods, err := authMethods(Target{AuthMethod: "password", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) < 2 {
		t.Fatalf("want password + keyboard-interactive, got %d methods", len(methods))
	}
}

func TestRunKeyboardInteractiveOnlyServer(t *testing.T) {
	srv := newTestSSHServer(t, func(s *testSSHServer) {
		s.requireKbdInt = true
		s.execOutput = "whoami-ok\n"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _, err := Run(ctx, srv.target(), "whoami")
	if err != nil {
		t.Fatalf("Run with kbd-int server: %v", err)
	}
	if out != "whoami-ok\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestRunTimeoutReturnsErrTimeoutAndPartialSafe(t *testing.T) {
	srv := newTestSSHServer(t, func(s *testSSHServer) {
		s.delayExec = 2 * time.Second
		s.execOutput = "never-seen\n"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := Run(ctx, srv.target(), "systemctl restart slow")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		// ErrTimeout wraps the cause
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout wrapper", err)
		}
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want errors.Is ErrTimeout", err)
	}
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	srv := newTestSSHServer(t)
	dir := t.TempDir()
	local := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(local, []byte("payload-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	n, _, err := Upload(ctx, srv.target(), local, "/data/hello.txt")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if n != int64(len("payload-bytes")) {
		t.Fatalf("uploaded %d bytes", n)
	}

	gotPath := filepath.Join(dir, "out.txt")
	n2, _, err := Download(ctx, srv.target(), "/data/hello.txt", gotPath)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if n2 != n {
		t.Fatalf("downloaded %d, uploaded %d", n2, n)
	}
	body, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "payload-bytes" {
		t.Fatalf("body = %q", body)
	}
}

func TestCleanRemotePath(t *testing.T) {
	cases := map[string]string{
		"/etc/nginx/nginx.conf": "/etc/nginx/nginx.conf",
		"app/config.yml":        "app/config.yml",
		"./x":                   "x",
		"":                      "",
		"  /a/b  ":              "/a/b",
	}
	for in, want := range cases {
		if got := cleanRemotePath(in); got != want {
			t.Errorf("cleanRemotePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCopyWithContextCancels(t *testing.T) {
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		// keep writer open briefly
		time.Sleep(100 * time.Millisecond)
		_ = pw.Close()
	}()
	// Blocked reader: write nothing until cancel
	var dst writeCounter
	_, err := copyWithContext(ctx, &dst, pr)
	if err == nil {
		t.Fatal("expected cancel error")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
}

type writeCounter struct{ n int }

func (w *writeCounter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}
