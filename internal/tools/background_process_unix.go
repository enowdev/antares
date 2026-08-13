//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	// Sandboxed commands may already carry Cloneflags, uid mappings, or a
	// parent-death signal. Preserve those settings while adding the process
	// group needed to terminate a command tree on timeout.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalProcessGroup sends sig to the command's own process group, falling
// back to the single process when that group cannot be confirmed.
//
// The fallback is the point. kill(-pid) addresses "the group led by pid", so
// if the child never became a group leader its PGID is still ours, and the
// negative kill would take down the antares daemon along with every session.
// Every construction path calls configureProcessGroup today, but nothing in
// the type system enforces that; verifying the PGID keeps a future shell
// backend that forgets it from turning a routine session reap into suicide.
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil || pgid == syscall.Getpgrp() || pgid <= 1 {
		_ = cmd.Process.Signal(sig)
		return
	}
	_ = syscall.Kill(-pgid, sig)
}

func terminateProcessGroup(cmd *exec.Cmd) {
	// Signal the whole group so child analyzers/build tools do not survive a
	// cancellation.
	signalProcessGroup(cmd, syscall.SIGTERM)
}

func killProcessGroup(cmd *exec.Cmd) {
	signalProcessGroup(cmd, syscall.SIGKILL)
}
