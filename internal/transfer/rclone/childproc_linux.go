//go:build linux

package rclone

import (
	"os/exec"
	"syscall"
)

// childSysProcAttr puts the child in its own process group (so a
// signal reaches rclone AND anything it spawned, and the stderr pipe
// EOFs when the group dies) and asks the kernel to kill it if this
// process dies first (§26.4: no orphan writer racing the next
// startup's reconciliation).
func childSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func signalChild(cmd *exec.Cmd, sig syscall.Signal) {
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

func killChild(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
