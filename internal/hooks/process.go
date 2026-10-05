package hooks

import (
	"os"
	"os/exec"
)

// LaunchDetached closes every inherited stream so hook hosts can finish as soon
// as Schedule returns. The worker reports errors to its bounded private log.
func LaunchDetached(executable, directory, job string) error {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	command := exec.Command(executable, "__hook-worker", "--state-dir", directory, "--job", job)
	command.Stdin, command.Stdout, command.Stderr = null, null, null
	detach(command)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
