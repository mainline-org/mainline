//go:build windows

package webhook

import (
	"os/exec"
	"syscall"
)

// detachAttrs is the Windows variant of the parent-survival flag.
// CREATE_NEW_PROCESS_GROUP gives the child its own process group so
// CTRL+C delivered to the parent console does not propagate. The
// DETACHED_PROCESS flag would let the child outlive the console
// entirely, which is what we want for a fire-and-forget HTTP sender.
func detachAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	const detachedProcess = 0x00000008
	const createNewProcessGroup = 0x00000200
	cmd.SysProcAttr.CreationFlags |= detachedProcess | createNewProcessGroup
}
