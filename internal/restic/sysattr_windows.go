//go:build windows

package restic

import "os/exec"

func setSysProcAttr(cmd *exec.Cmd) {}
