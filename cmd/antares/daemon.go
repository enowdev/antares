package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/version"
)

var (
	daemonReadyTimeout = 30 * time.Second
	daemonStopTimeout  = 15 * time.Second
)

type daemonState struct {
	PID       int    `json:"pid"`
	StartTime string `json:"start_time,omitempty"`
	Exe       string `json:"exe"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
	Managed   bool   `json:"managed,omitempty"`
}

func daemonPIDFile() string { return config.Path("antares.pid") }
func daemonLogFile() string { return config.Path("logs", "daemon.log") }
func daemonLockDir() string { return config.Path("antares.start.lock") }

func cmdServe(args []string) error {
	foreground := false
	for _, arg := range args {
		switch arg {
		case "--foreground", "-f":
			foreground = true
		case "--background", "-d", "--daemon":
			// Background is the default; retain explicit aliases for scripts.
		case "--help", "-h":
			fmt.Println("usage: antares [--foreground]")
			return nil
		default:
			return fmt.Errorf("unknown serve option %q", arg)
		}
	}
	if foreground {
		return cmdServeForeground()
	}
	return startDaemon()
}

func startDaemon() error {
	if err := config.EnsureHome(); err != nil {
		return err
	}
	release, err := acquireDaemonStartLock(5 * time.Second)
	if err != nil {
		return err
	}
	defer release()
	if state, live, err := currentDaemon(); err != nil {
		return err
	} else if live {
		fmt.Printf("Antares is already running (pid %d) at %s\n", state.PID, state.URL)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	url := daemonURL(cfg)
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating antares executable: %w", err)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	logPath := daemonLogFile()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening daemon log: %w", err)
	}
	if err := logFile.Chmod(0o600); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("securing daemon log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "_serve_foreground")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	configureDaemonProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting antares daemon: %w", err)
	}
	state := daemonState{
		PID: cmd.Process.Pid, Exe: exe, URL: url, Version: version.Version,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Managed: true,
	}
	state.StartTime, err = processStartTime(state.PID)
	if err != nil {
		_ = terminateDaemonProcess(cmd.Process, true)
		return fmt.Errorf("inspecting daemon process: %w", err)
	}
	if err := writeDaemonState(state); err != nil {
		_ = terminateDaemonProcess(cmd.Process, true)
		return err
	}
	_ = cmd.Process.Release()

	if err := waitDaemonReady(state, daemonReadyTimeout); err != nil {
		_ = stopDaemonState(state, 3*time.Second)
		_ = os.Remove(daemonPIDFile())
		return fmt.Errorf("daemon failed to become ready: %w (see %s)", err, logPath)
	}
	fmt.Printf("Antares started in background (pid %d)\n", state.PID)
	fmt.Printf("Dashboard: %s\n", state.URL)
	fmt.Printf("Logs: %s\n", logPath)
	fmt.Println("Stop with: antares stop")
	return nil
}

func daemonURL(cfg *config.Config) string {
	host := cfg.Server.Host
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(cfg.Server.Port))
}

func acquireDaemonStartLock(timeout time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	for {
		err := os.Mkdir(daemonLockDir(), 0o700)
		if err == nil {
			return func() { _ = os.Remove(daemonLockDir()) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("creating daemon start lock: %w", err)
		}
		if info, statErr := os.Stat(daemonLockDir()); statErr == nil && time.Since(info.ModTime()) > daemonReadyTimeout+5*time.Second {
			_ = os.Remove(daemonLockDir()) // abandoned by a crashed starter
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("another antares command is still starting the daemon")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func cmdStop(args []string) error {
	if len(args) > 0 {
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			fmt.Println("usage: antares stop")
			return nil
		}
		return fmt.Errorf("unknown stop option %q", args[0])
	}
	state, live, err := currentDaemon()
	if err != nil {
		return err
	}
	if !live {
		fmt.Println("Antares is not running")
		return nil
	}
	fmt.Printf("Stopping Antares (pid %d)...\n", state.PID)
	if err := stopDaemonState(state, daemonStopTimeout); err != nil {
		return err
	}
	_ = os.Remove(daemonPIDFile())
	fmt.Println("Antares stopped")
	return nil
}

func cmdStatus(args []string) error {
	if len(args) > 0 {
		return errors.New("usage: antares status")
	}
	state, live, err := currentDaemon()
	if err != nil {
		return err
	}
	if !live {
		fmt.Println("Antares is stopped")
		return nil
	}
	fmt.Printf("Antares is running (pid %d)\n", state.PID)
	fmt.Printf("Dashboard: %s\n", state.URL)
	fmt.Printf("Version: %s\n", state.Version)
	fmt.Printf("Logs: %s\n", daemonLogFile())
	return nil
}

func currentDaemon() (daemonState, bool, error) {
	state, err := readDaemonState()
	if errors.Is(err, os.ErrNotExist) {
		return discoverLegacyDaemon()
	}
	if err != nil {
		return daemonState{}, false, err
	}
	live, err := validateDaemonState(state)
	if err != nil {
		return daemonState{}, false, err
	}
	if !live {
		_ = os.Remove(daemonPIDFile())
		return discoverLegacyDaemon()
	}
	return state, true, nil
}

func discoverLegacyDaemon() (daemonState, bool, error) {
	// Recover a running server that has no state file — e.g. its antares.pid was
	// removed, or it was started by an older binary. Works on every platform
	// that implements the process/port helpers; where they return nothing (an
	// unsupported OS) this simply finds no daemon.
	cfg, err := config.Load()
	if err != nil {
		return daemonState{}, false, err
	}
	url := daemonURL(cfg)
	pids, err := processesListeningOnPort(cfg.Server.Port)
	if err != nil {
		return daemonState{}, false, err
	}
	for _, pid := range pids {
		if pid == os.Getpid() {
			continue
		}
		owner, err := processOwnerUID(pid)
		if err != nil || owner != os.Getuid() {
			continue
		}
		args, err := processArguments(pid)
		if err != nil || !isAntaresServerArgs(args) {
			continue
		}
		exe, err := processExecutable(pid)
		if err != nil || !strings.Contains(strings.ToLower(filepath.Base(exe)), "antares") {
			continue
		}
		if !healthResponds(url) {
			continue
		}
		start, err := processStartTime(pid)
		if err != nil {
			continue
		}
		state := daemonState{PID: pid, StartTime: start, Exe: exe, URL: url, Version: "legacy"}
		_ = writeDaemonState(state)
		return state, true, nil
	}
	return daemonState{}, false, nil
}

func isAntaresServerArgs(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[1] == "_serve_foreground" || args[1] == "serve" || args[1] == "start"
}

func healthResponds(url string) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(strings.TrimRight(url, "/") + "/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func writeDaemonState(state daemonState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := daemonPIDFile()
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readDaemonState() (daemonState, error) {
	b, err := os.ReadFile(daemonPIDFile())
	if err != nil {
		return daemonState{}, err
	}
	var state daemonState
	if err := json.Unmarshal(b, &state); err != nil {
		return daemonState{}, fmt.Errorf("invalid daemon state %s: %w", daemonPIDFile(), err)
	}
	if state.PID <= 1 || state.Exe == "" {
		return daemonState{}, fmt.Errorf("invalid daemon state %s", daemonPIDFile())
	}
	return state, nil
}

func validateDaemonState(state daemonState) (bool, error) {
	alive, err := processAlive(state.PID)
	if err != nil || !alive {
		return false, err
	}
	start, err := processStartTime(state.PID)
	if err != nil {
		return false, err
	}
	if state.StartTime != "" && start != state.StartTime {
		return false, nil // PID has been reused.
	}
	owner, err := processOwnerUID(state.PID)
	if err != nil {
		return false, err
	}
	if owner != os.Getuid() {
		return false, nil
	}
	exe, err := processExecutable(state.PID)
	if err != nil {
		return false, err
	}
	wantExe, _ := filepath.EvalSymlinks(state.Exe)
	if exe != wantExe {
		return false, nil
	}
	args, err := processArguments(state.PID)
	if err != nil {
		return false, err
	}
	return isAntaresServerArgs(args), nil
}

func waitDaemonReady(state daemonState, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 800 * time.Millisecond}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	for time.Now().Before(deadline) {
		live, err := validateDaemonState(state)
		if err != nil {
			return err
		}
		if !live {
			return errors.New("process exited during startup")
		}
		owners, ownerErr := processesListeningOnPort(cfg.Server.Port)
		if ownerErr != nil {
			return ownerErr
		}
		ownsListener := false
		for _, pid := range owners {
			if pid == state.PID {
				ownsListener = true
				break
			}
		}
		if !ownsListener {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		resp, err := client.Get(strings.TrimRight(state.URL, "/") + "/api/health")
		if err == nil {
			var health struct {
				OK      bool   `json:"ok"`
				Version string `json:"version"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&health)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil && health.OK && health.Version == version.Version {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s", timeout)
}

func stopDaemonState(state daemonState, timeout time.Duration) error {
	live, err := validateDaemonState(state)
	if err != nil {
		return err
	}
	if !live {
		return nil
	}
	proc, err := os.FindProcess(state.PID)
	if err != nil {
		return err
	}
	if err := terminateDaemonProcess(proc, state.Managed); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("sending SIGTERM: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive, _ := daemonTargetAlive(state.PID, state.Managed)
		if !alive {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := killDaemonProcess(proc, state.Managed); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("daemon did not stop after %s; SIGKILL failed: %w", timeout, err)
	}
	for i := 0; i < 20; i++ {
		alive, _ := daemonTargetAlive(state.PID, state.Managed)
		if !alive {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon pid %d is still alive after SIGKILL", state.PID)
}
