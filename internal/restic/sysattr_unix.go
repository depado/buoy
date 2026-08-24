//go:build !windows

package restic

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// gracefulKill sends SIGTERM so the child can clean up (restic removes its
// repo lock on SIGTERM), escalating to SIGKILL after grace.
func gracefulKill(p *os.Process, grace time.Duration) {
	p.Signal(syscall.SIGTERM) //nolint:errcheck
	time.AfterFunc(grace, func() {
		p.Kill() //nolint:errcheck
	})
}
