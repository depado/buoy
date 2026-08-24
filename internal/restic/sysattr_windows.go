//go:build windows

package restic

import (
	"os"
	"os/exec"
	"time"
)

func setSysProcAttr(cmd *exec.Cmd) {}

// gracefulKill has no SIGTERM on Windows; the child is SIGKILLed directly.
func gracefulKill(p *os.Process, grace time.Duration) {
	p.Kill() //nolint:errcheck
}
