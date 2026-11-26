//go:build !windows

package sessions

import "os/exec"

func configureEnvironmentCommand(command *exec.Cmd) {}
