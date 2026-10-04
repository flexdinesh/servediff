package browser

import (
	"reflect"
	"testing"
)

func TestBrowserCommandSupportsDesktopEnvironments(t *testing.T) {
	url := "http://localhost:7981/contexts/observation"
	for _, test := range []struct {
		name      string
		platform  string
		env       map[string]string
		command   string
		arguments []string
	}{
		{"macOS", "darwin", nil, "open", []string{url}},
		{"X11", "linux", map[string]string{"DISPLAY": ":0"}, "xdg-open", []string{url}},
		{"Wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "xdg-open", []string{url}},
		{"headless", "linux", nil, "", nil},
		{"Windows", "windows", nil, "rundll32.exe", []string{"url.dll,FileProtocolHandler", url}},
		{"unsupported", "plan9", nil, "", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, arguments := browserCommand(test.platform, func(key string) string { return test.env[key] }, url)
			if command != test.command || !reflect.DeepEqual(arguments, test.arguments) {
				t.Fatalf("command = %s %v, want %s %v", command, arguments, test.command, test.arguments)
			}
		})
	}
}

func TestBrowserSkipsSSHIncludingForwardedDisplays(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		for _, sshKey := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
			t.Run(platform+"/"+sshKey, func(t *testing.T) {
				env := map[string]string{sshKey: "connected", "DISPLAY": ":10", "WAYLAND_DISPLAY": "wayland-0"}
				command, _ := browserCommand(platform, func(key string) string { return env[key] }, "http://localhost:7981/contexts/observation")
				if command != "" {
					t.Fatalf("SSH session would launch %s", command)
				}
			})
		}
	}
}

func TestOpenSkipsMissingLauncher(t *testing.T) {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		t.Setenv(key, "")
	}
	t.Setenv("DISPLAY", ":0")
	t.Setenv("PATH", t.TempDir())
	if err := Open("http://localhost:7981/contexts/observation"); err != nil {
		t.Fatalf("missing launcher should be skipped: %v", err)
	}
}
