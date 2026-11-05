//go:build !windows

package daemon

import (
	"errors"
	"syscall"
)

// processAlive: signal 0 succeeds, or fails with EPERM (alive, not ours).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
