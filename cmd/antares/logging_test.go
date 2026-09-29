package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

// TestTUILogsStayOffTheTerminal: with logToConsole off (antares tui), a log
// line goes to the log file and not to stderr, where it would print over the
// interface; stderr itself is left as it was.
func TestTUILogsStayOffTheTerminal(t *testing.T) {
	prevLogger, prevFlag, prevStderr := slog.Default(), logToConsole, os.Stderr
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		logToConsole = prevFlag
		os.Stderr = prevStderr
	})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	logFile := filepath.Join(t.TempDir(), "antares.log")
	cfg := &config.Config{Logging: config.Logging{Level: "info", File: logFile}}

	logToConsole = false
	if err := setupLogging(cfg); err != nil {
		t.Fatal(err)
	}
	if os.Stderr != w {
		t.Fatal("setupLogging must put stderr back")
	}
	slog.Info("installed the bundled skills", "count", 3)
	_ = w.Close()
	printed, _ := io.ReadAll(r)
	if len(printed) != 0 {
		t.Errorf("a log line reached the terminal: %q", printed)
	}
	data, _ := os.ReadFile(logFile)
	if !strings.Contains(string(data), "installed the bundled skills") {
		t.Errorf("the log file is missing the line: %q", data)
	}
}
