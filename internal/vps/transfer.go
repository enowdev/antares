package vps

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
)

// MaxTransferBytes caps a single upload or download so a runaway path cannot
// exhaust memory or fill the workspace. 256 MiB is enough for configs, modest
// builds, and logs; larger payloads should use a remote pull (curl/wget) or
// rsync via vps_run.
const MaxTransferBytes = 256 << 20

// Upload copies a local file to remotePath on the VPS over SFTP. Parent
// directories on the remote side are created as needed. Returns bytes written
// and the host key seen (for TOFU pinning).
func Upload(ctx context.Context, t Target, localPath, remotePath string) (n int64, seen string, err error) {
	localPath = filepath.Clean(localPath)
	remotePath = cleanRemotePath(remotePath)
	if remotePath == "" {
		return 0, "", fmt.Errorf("remote_path is required")
	}
	fi, err := os.Stat(localPath)
	if err != nil {
		return 0, "", fmt.Errorf("local file: %w", err)
	}
	if fi.IsDir() {
		return 0, "", fmt.Errorf("local path is a directory; upload a single file")
	}
	if fi.Size() > MaxTransferBytes {
		return 0, "", fmt.Errorf("local file is %d bytes; max is %d — use a remote pull for larger payloads", fi.Size(), MaxTransferBytes)
	}

	client, err := dial(ctx, t)
	if err != nil {
		return 0, "", err
	}
	defer client.Close()
	seen = client.seenHostKey

	sc, err := sftp.NewClient(client.Client)
	if err != nil {
		return 0, seen, fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()

	if dir := path.Dir(remotePath); dir != "" && dir != "." {
		if err := sc.MkdirAll(dir); err != nil {
			return 0, seen, fmt.Errorf("create remote dir %s: %w", dir, err)
		}
	}

	src, err := os.Open(localPath)
	if err != nil {
		return 0, seen, err
	}
	defer src.Close()

	dst, err := sc.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, seen, fmt.Errorf("open remote %s: %w", remotePath, err)
	}
	defer dst.Close()

	n, err = copyWithContext(ctx, dst, src)
	if err != nil {
		return n, seen, err
	}
	// Preserve mode bits the local file had (best-effort; some servers refuse).
	_ = sc.Chmod(remotePath, fi.Mode().Perm())
	return n, seen, nil
}

// Download copies remotePath from the VPS into localPath over SFTP. Parent
// directories on the local side are created as needed. Returns bytes written
// and the host key seen.
func Download(ctx context.Context, t Target, remotePath, localPath string) (n int64, seen string, err error) {
	localPath = filepath.Clean(localPath)
	remotePath = cleanRemotePath(remotePath)
	if remotePath == "" {
		return 0, "", fmt.Errorf("remote_path is required")
	}

	client, err := dial(ctx, t)
	if err != nil {
		return 0, "", err
	}
	defer client.Close()
	seen = client.seenHostKey

	sc, err := sftp.NewClient(client.Client)
	if err != nil {
		return 0, seen, fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()

	fi, err := sc.Stat(remotePath)
	if err != nil {
		return 0, seen, fmt.Errorf("remote file: %w", err)
	}
	if fi.IsDir() {
		return 0, seen, fmt.Errorf("remote path is a directory; download a single file")
	}
	if fi.Size() > MaxTransferBytes {
		return 0, seen, fmt.Errorf("remote file is %d bytes; max is %d", fi.Size(), MaxTransferBytes)
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return 0, seen, fmt.Errorf("create local dir: %w", err)
	}

	src, err := sc.Open(remotePath)
	if err != nil {
		return 0, seen, fmt.Errorf("open remote %s: %w", remotePath, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return 0, seen, err
	}
	defer func() {
		_ = dst.Close()
	}()

	n, err = copyWithContext(ctx, dst, src)
	return n, seen, err
}

// cleanRemotePath normalises a remote path for SFTP (slash-separated). Absolute
// paths stay absolute; relative paths stay relative so they resolve under the
// SSH user's home when the server supports it.
func cleanRemotePath(orig string) string {
	orig = strings.TrimSpace(orig)
	if orig == "" {
		return ""
	}
	orig = filepath.ToSlash(orig)
	abs := strings.HasPrefix(orig, "/")
	cleaned := path.Clean(orig)
	if cleaned == "." {
		return ""
	}
	if abs && !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

// copyWithContext streams src to dst, aborting if ctx is cancelled, and
// enforcing MaxTransferBytes. Reads run in a goroutine so a blocked Read does
// not ignore cancellation (plain io.Copy cannot be cancelled).
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	type readRes struct {
		n   int
		err error
	}
	for {
		if err := ctx.Err(); err != nil {
			return written, fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		ch := make(chan readRes, 1)
		go func() {
			n, err := src.Read(buf)
			ch <- readRes{n: n, err: err}
		}()
		var rr readRes
		select {
		case <-ctx.Done():
			return written, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
		case rr = <-ch:
		}
		if rr.n > 0 {
			if written+int64(rr.n) > MaxTransferBytes {
				return written, fmt.Errorf("transfer exceeds max of %d bytes", MaxTransferBytes)
			}
			nw, ew := dst.Write(buf[0:rr.n])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if rr.n != nw {
				return written, io.ErrShortWrite
			}
		}
		if rr.err == io.EOF {
			return written, nil
		}
		if rr.err != nil {
			return written, rr.err
		}
	}
}
