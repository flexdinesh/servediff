package browser

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func Open(url string) error {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return nil
	}
	command := ""
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "linux":
		command = "xdg-open"
	default:
		return nil
	}
	process := exec.Command(command, url)
	process.Stdin, process.Stdout, process.Stderr = nil, nil, nil
	if err := process.Start(); err != nil {
		return fmt.Errorf("unable to open browser with %s: %w", command, err)
	}
	return process.Process.Release()
}
