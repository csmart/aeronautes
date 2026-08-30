//go:build windows

package rclone

import (
	"os/exec"
	"syscall"
)

// Windows has no Unix signals or process groups in this form; the
// graceful half is skipped and cancellation kills the child directly.
// Job-object containment is §65.10 scope and is tracked, not faked.
func childSysProcAttr() *syscall.SysProcAttr { return nil }

func signalChild(cmd *exec.Cmd, sig syscall.Signal) {
	_ = cmd.Process.Kill()
}

func killChild(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
