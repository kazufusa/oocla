//go:build unix

package claudecli

import (
	"os/exec"
	"syscall"
)

// isolateGroup puts the child in its own process group so that its whole tree
// can be signalled at once.
func isolateGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup signals the process group led by pid. It reports whether the signal
// was delivered.
func killGroup(pid int) bool {
	return syscall.Kill(-pid, syscall.SIGKILL) == nil
}
