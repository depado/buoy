//go:build !unix

package restic

import "os/exec"

func detachProcessGroup(cmd *exec.Cmd) {}
