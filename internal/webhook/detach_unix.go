//go:build !windows

package webhook

import (
	"os/exec"
	"syscall"
)

// detachAttrs configures cmd so the child process does not die when
// the parent CLI exits. setsid() puts the child in a brand new
// session and process group so SIGINT/SIGHUP delivered to the
// parent's group does not propagate.
//
// We deliberately do NOT chroot, redirect to /dev/null, or daemonize
// fully — the child is a transient HTTP sender with a short lifespan,
// and stdio is already nil-ed out by the caller.
func detachAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
