//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts the dashboard in its own process group, so it keeps running
// when the agent closes micelio mcp.
func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
