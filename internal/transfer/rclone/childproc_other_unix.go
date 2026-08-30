//go:build unix && !linux

package rclone

import (
	"os/exec"
	"syscall"
)

// Process-group containment without Pdeathsig (Linux-only); macOS
// relies on the group kill plus startup reconciliation (§65.10 tracks
// full parent-death coupling per platform).
func childSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func signalChild(cmd *exec.Cmd, sig syscall.Signal) {
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

func killChild(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
