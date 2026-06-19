//go:build unix

package restic

import (
	"os/exec"
	"syscall"
)

// detachProcessGroup sets the child process to run in its own process group
// so it does not receive signals sent to the parent's process group.
func detachProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
