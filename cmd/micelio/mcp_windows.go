//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detach starts the dashboard in its own process group without a console,
// so it keeps running when the agent closes micelio mcp.
func detach(c *exec.Cmd) {
	const detachedProcess = 0x00000008
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
