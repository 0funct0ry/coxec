package engine

import (
	"os"
	"os/exec"
)

// RunShellCommand executes a single command in the default shell
// Inherits standard output and standard error from the host process
func RunShellCommand(command string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
