package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type logsOptions struct {
	Follow bool
	Lines  int
	Path   bool
}

func parseLogsArgs(args []string) (logsOptions, error) {
	o := logsOptions{Lines: 50}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-f" || a == "--follow":
			o.Follow = true
		case a == "--path":
			o.Path = true
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs a number", a)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return o, fmt.Errorf("%s wants a non-negative number, got %q", a, args[i])
			}
			o.Lines = n
		case strings.HasPrefix(a, "-n") && len(a) > 2:
			// tail-style -n100
			n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(a, "-n"), "="))
			if err != nil || n < 0 {
				return o, fmt.Errorf("-n wants a non-negative number, got %q", a)
			}
			o.Lines = n
		case strings.HasPrefix(a, "--lines="):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "--lines="))
			if err != nil || n < 0 {
				return o, fmt.Errorf("--lines wants a non-negative number, got %q", a)
			}
			o.Lines = n
		default:
			return o, fmt.Errorf("unknown logs option %q (usage: antares logs [-f] [-n N] [--path])", a)
		}
	}
	return o, nil
}

// cmdLogs prints the tail of the background server's log, and with -f keeps
// following it like tail -f.
func cmdLogs(args []string) error {
	opts, err := parseLogsArgs(args)
	if err != nil {
		return err
	}
	path := daemonLogFile()
	if opts.Path {
		fmt.Println(path)
		return nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) && !opts.Follow {
		return fmt.Errorf("no log at %s yet — it is written once the background server has started", path)
	}
	var offset int64
	if err == nil {
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		tail, err := lastLines(f, fi.Size(), opts.Lines)
		if err != nil {
			f.Close()
			return err
		}
		_, _ = os.Stdout.Write(tail)
		offset = fi.Size()
		f.Close()
	}
	if !opts.Follow {
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return followFile(ctx, path, offset, os.Stdout, 250*time.Millisecond)
}

// lastLines returns the final n lines of r (whose length is size), reading
// backwards in blocks so a large log is not read whole.
func lastLines(r io.ReaderAt, size int64, n int) ([]byte, error) {
	if n <= 0 || size == 0 {
		return nil, nil
	}
	const block = 32 << 10
	var buf []byte
	pos := size
	for pos > 0 {
		step := int64(block)
		if pos < step {
			step = pos
		}
		pos -= step
		chunk := make([]byte, step)
		if _, err := r.ReadAt(chunk, pos); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		buf = append(chunk, buf...)
		// One more newline than lines wanted, ignoring a trailing one, means
		// the start of the n-th last line is in hand.
		if bytes.Count(bytes.TrimSuffix(buf, []byte("\n")), []byte("\n")) >= n {
			break
		}
	}
	body := bytes.TrimSuffix(buf, []byte("\n"))
	idx := len(body)
	for i := 0; i < n; i++ {
		j := bytes.LastIndexByte(body[:idx], '\n')
		if j < 0 {
			idx = -1
			break
		}
		idx = j
	}
	return buf[idx+1:], nil
}

// followFile streams what is appended to path from offset until ctx ends. A
// truncated or replaced file (a restart that rewrote the log) is read again
// from its start; a missing one is waited for.
func followFile(ctx context.Context, path string, offset int64, w io.Writer, every time.Duration) error {
	var f *os.File
	var info os.FileInfo
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		cur, err := os.Stat(path)
		switch {
		case err != nil:
			// Not there (yet, or rotated away): keep waiting.
		case f == nil || (info != nil && !os.SameFile(info, cur)):
			if f != nil {
				f.Close()
				offset = 0
			}
			if f, err = os.Open(path); err != nil {
				f = nil
				break
			}
			info = cur
			if offset > cur.Size() {
				offset = 0
			}
		}
		if f != nil && cur != nil {
			if cur.Size() < offset {
				offset = 0 // truncated in place
			}
			if cur.Size() > offset {
				n, err := io.Copy(w, io.NewSectionReader(f, offset, cur.Size()-offset))
				offset += n
				if err != nil {
					return err
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
