//go:build !windows

package tailcompat

import (
	"errors"
	"syscall"
)

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// usePolling reports whether the follow backend should poll instead of using
// native fs notifications.
func usePolling() bool { return false }
