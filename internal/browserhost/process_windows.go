//go:build windows

package browserhost

import "os/exec"

func configureProcess(*exec.Cmd) {}

func killProcess(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}
