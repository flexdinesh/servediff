package browser

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func Open(url string) error {
	command, arguments := browserCommand(runtime.GOOS, os.Getenv, url)
	if command == "" {
		return nil
	}
	if _, err := exec.LookPath(command); err != nil {
		return nil
	}
	process := exec.Command(command, arguments...)
	process.Stdin, process.Stdout, process.Stderr = nil, nil, nil
	if err := process.Start(); err != nil {
		return fmt.Errorf("unable to open browser with %s: %w", command, err)
	}
	return process.Process.Release()
}

func browserCommand(platform string, getenv func(string) string, url string) (string, []string) {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if getenv(key) != "" {
			return "", nil
		}
	}
	switch platform {
	case "darwin":
		return "open", []string{url}
	case "linux":
		if getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != "" {
			return "xdg-open", []string{url}
		}
	case "windows":
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", url}
	}
	return "", nil
}
