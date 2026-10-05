package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testInstaller(t *testing.T) (installer, *[][]string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "home with ' spaces")
	i := installer{
		home: root, data: filepath.Join(root, "data"), config: filepath.Join(root, "config"),
		source: filepath.Join(root, "repository"), binary: filepath.Join(root, "bin", "servediff"),
		configFile: filepath.Join(root, "machine config.json"), env: func(string) string { return "" },
	}
	calls := new([][]string)
	i.run = func(name string, args ...string) error {
		*calls = append(*calls, append([]string{name}, args...))
		return nil
	}
	i.query = func(name string, args ...string) ([]byte, error) {
		registered := false
		for _, call := range *calls {
			if len(call) >= 4 && call[0] == "codex" && call[2] == "marketplace" {
				registered = call[3] == "add"
			}
		}
		if registered {
			return json.Marshal(map[string]any{"marketplaces": []any{map[string]any{
				"name": marketplace, "root": i.root("codex"), "marketplaceSource": map[string]string{"sourceType": "local"},
			}}})
		}
		return []byte(`{"marketplaces":[]}`), nil
	}
	for _, host := range []string{"codex", "claude", "opencode", "pi"} {
		packageRoot := filepath.Join(i.source, "packages", "plugin-"+host)
		if host == "codex" || host == "claude" {
			put(t, filepath.Join(packageRoot, "hooks", "hooks.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"servediff hook --agent `+host+` >/dev/null 2>&1 || true","timeout":5}]}]}}`)
			put(t, filepath.Join(packageRoot, "."+host+"-plugin", "plugin.json"), `{"name":"servediff"}`)
		} else {
			put(t, filepath.Join(packageRoot, "dist", "index.js"), `let settings; export function configure(value) {settings=value;} export default function () { return settings; }`)
			put(t, filepath.Join(packageRoot, "package.json"), `{"type":"module","pi":{"extensions":["./dist/index.js"]}}`)
		}
		put(t, filepath.Join(packageRoot, "node_modules", "workspace-package", "index.js"), "not portable")
	}
	return i, calls
}

func put(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestInstallRemovePreservesUnrelatedConfigurationAndPlugins(t *testing.T) {
	for _, host := range []string{"codex", "claude", "opencode", "pi"} {
		t.Run(host, func(t *testing.T) {
			i, calls := testInstaller(t)
			settings := filepath.Join(i.home, "settings.json")
			settingsContents := `{"theme":"dark","plugins":["unrelated"]}`
			put(t, settings, settingsContents)
			otherPlugin := filepath.Join(filepath.Dir(i.root(host)), "other-plugin", "plugin.json")
			put(t, otherPlugin, `{"name":"other-plugin"}`)
			for range 2 {
				if err := i.install(host); err != nil {
					t.Fatal(err)
				}
			}
			if got := read(t, settings); got != settingsContents {
				t.Fatalf("settings changed: %s", got)
			}
			if got := read(t, otherPlugin); got != `{"name":"other-plugin"}` {
				t.Fatal("unrelated plugin changed")
			}
			if _, err := os.Stat(filepath.Join(i.root(host), "node_modules")); !os.IsNotExist(err) {
				t.Fatal("copied workspace dependencies")
			}
			if host == "opencode" {
				entry := read(t, i.openCodeFile())
				binary, _ := json.Marshal(i.binary)
				config, _ := json.Marshal(i.configFile)
				if !strings.Contains(entry, string(binary)) || !strings.Contains(entry, string(config)) {
					t.Fatal("installed entry missing absolute binary/config")
				}
			}
			for range 2 {
				if err := i.remove(host); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(i.root(host)); !os.IsNotExist(err) {
				t.Fatal("managed plugin remains")
			}
			if host == "opencode" {
				if _, err := os.Stat(i.openCodeFile()); !os.IsNotExist(err) {
					t.Fatal("loaded OpenCode entry remains")
				}
			}
			if read(t, settings) != settingsContents || read(t, otherPlugin) != `{"name":"other-plugin"}` {
				t.Fatal("remove modified unrelated config/plugin")
			}
			if host == "codex" {
				expected := [][]string{
					{"codex", "plugin", "marketplace", "add", i.root(host)},
					{"codex", "plugin", "add", "servediff@servediff-local"},
					{"codex", "plugin", "add", "servediff@servediff-local"},
					{"codex", "plugin", "remove", "servediff@servediff-local"},
					{"codex", "plugin", "marketplace", "remove", "servediff-local"},
				}
				if !reflect.DeepEqual(*calls, expected) {
					t.Fatalf("native registration: %v", *calls)
				}
			}
		})
	}
}

func TestUnmanagedPathsNeverReplacedOrRemoved(t *testing.T) {
	for _, host := range []string{"codex", "claude", "opencode", "pi"} {
		t.Run(host, func(t *testing.T) {
			i, calls := testInstaller(t)
			file := filepath.Join(i.root(host), "user.txt")
			put(t, file, "user owned")
			if err := i.install(host); err == nil {
				t.Fatal("install overwrote unmanaged plugin")
			}
			if err := i.remove(host); err == nil {
				t.Fatal("remove deleted unmanaged plugin")
			}
			if len(*calls) != 0 || read(t, file) != "user owned" {
				t.Fatal("unmanaged path changed")
			}
		})
	}
}

func TestOpenCodeEntryCollisionPreserved(t *testing.T) {
	i, _ := testInstaller(t)
	put(t, i.openCodeFile(), "// user's servediff integration")
	if err := i.install("opencode"); err == nil {
		t.Fatal("install accepted collision")
	}
	if err := i.remove("opencode"); err == nil {
		t.Fatal("remove accepted collision")
	}
	if got := read(t, i.openCodeFile()); got != "// user's servediff integration" {
		t.Fatal("existing entry overwritten")
	}
}

func TestHookCommandUsesAbsoluteQuotedPathsAndPreservesFailureIsolation(t *testing.T) {
	i, _ := testInstaller(t)
	if err := i.install("claude"); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string
				Timeout int
			}
		}
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(i.root("claude"), "hooks", "hooks.json"))), &document); err != nil {
		t.Fatal(err)
	}
	hook := document.Hooks["Stop"][0].Hooks[0]
	if hook.Timeout != 5 || !strings.Contains(hook.Command, quoteCommand(i.binary)) || !strings.Contains(hook.Command, quoteCommand(i.configFile)) {
		t.Fatalf("hook binding: %+v", hook)
	}
	if runtime.GOOS != "windows" && !strings.HasSuffix(hook.Command, "|| true") {
		t.Fatal("hook can block agent on failure")
	}
	if runtime.GOOS != "windows" {
		// Execute the native command: paths contain both spaces and an apostrophe.
		put(t, i.binary, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HOOK_TEST_ARGS\"\ncat > \"$HOOK_TEST_STDIN\"\nexit 37\n")
		if err := os.Chmod(i.binary, 0700); err != nil {
			t.Fatal(err)
		}
		argsFile := filepath.Join(i.home, "captured args")
		stdinFile := filepath.Join(i.home, "captured stdin")
		command := exec.Command("sh", "-c", hook.Command)
		command.Env = append(os.Environ(), "HOOK_TEST_ARGS="+argsFile, "HOOK_TEST_STDIN="+stdinFile)
		payload := `{"cwd":"/checkout with spaces","session_id":"session"}`
		command.Stdin = strings.NewReader(payload)
		if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
			t.Fatalf("hook leaked failure/output: %v %s", err, output)
		}
		if expected := "hook\n--agent\nclaude\n--config-file\n" + i.configFile + "\n"; read(t, argsFile) != expected {
			t.Fatal("hook arguments lost path quoting")
		}
		if read(t, stdinFile) != payload {
			t.Fatal("host completion JSON not forwarded")
		}
	}
}

func TestFailedPluginRegistrationRetriesWithoutReregisteringMarketplace(t *testing.T) {
	i, calls := testInstaller(t)
	success := i.run
	i.run = func(name string, args ...string) error {
		if len(args) == 3 && args[1] == "add" {
			return errors.New("install failed")
		}
		return success(name, args...)
	}
	if err := i.install("codex"); err == nil {
		t.Fatal("failed host command ignored")
	}
	i.run = success
	if err := i.install("codex"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0][2] != "marketplace" || (*calls)[1][2] != "add" {
		t.Fatalf("retry registered marketplace twice: %v", *calls)
	}
}

func TestCodexRegistrationRepairsRemovedMarketplaceAndPreservesConflicts(t *testing.T) {
	i, calls := testInstaller(t)
	if err := i.install("codex"); err != nil {
		t.Fatal(err)
	}
	// An external native uninstall invalidates registration independently of files.
	if err := i.run("codex", "plugin", "marketplace", "remove", marketplace); err != nil {
		t.Fatal(err)
	}
	if err := i.install("codex"); err != nil {
		t.Fatal(err)
	}
	if last := (*calls)[len(*calls)-2]; last[2] != "marketplace" || last[3] != "add" {
		t.Fatalf("marketplace was not repaired: %v", *calls)
	}
	i.query = func(string, ...string) ([]byte, error) {
		return []byte(`{"marketplaces":[{"name":"servediff-local","root":"/unrelated/user/marketplace","marketplaceSource":{"sourceType":"local"}}]}`), nil
	}
	before := len(*calls)
	for _, action := range []func(string) error{i.install, i.remove} {
		if err := action("codex"); err == nil {
			t.Fatal("unrelated marketplace accepted")
		}
	}
	if len(*calls) != before {
		t.Fatal("unrelated marketplace changed")
	}
}

func TestHostDirectoryOverrides(t *testing.T) {
	i, _ := testInstaller(t)
	i.env = func(key string) string {
		if key == "CLAUDE_CONFIG_DIR" {
			return filepath.Join(i.home, "custom claude")
		}
		return ""
	}
	if expected := filepath.Join(i.home, "custom claude", "skills", "servediff"); i.root("claude") != expected {
		t.Fatalf("config dir override ignored: %s", i.root("claude"))
	}
}

// Opt-in smoke checks use installed host CLIs, with entirely isolated host settings.
func TestNativeLocalInstall(t *testing.T) {
	if os.Getenv("SERVEDIFF_TEST_NATIVE_HOSTS") != "1" {
		t.Skip("set SERVEDIFF_TEST_NATIVE_HOSTS=1 to exercise installed Codex and Pi CLIs")
	}
	for _, host := range []string{"codex", "pi"} {
		t.Run(host, func(t *testing.T) {
			if _, err := exec.LookPath(host); err != nil {
				t.Skip("host CLI unavailable")
			}
			i, _ := testInstaller(t)
			isolatedConfig := filepath.Join(i.home, "host settings")
			varKey := "CODEX_HOME"
			if host == "pi" {
				varKey = "PI_CODING_AGENT_DIR"
			}
			if err := os.MkdirAll(isolatedConfig, 0700); err != nil {
				t.Fatal(err)
			}
			i.query = func(name string, args ...string) ([]byte, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, name, args...)
				for _, value := range os.Environ() {
					if !strings.HasPrefix(value, varKey+"=") {
						command.Env = append(command.Env, value)
					}
				}
				command.Env = append(command.Env, varKey+"="+isolatedConfig, "PI_OFFLINE=1", "PI_TELEMETRY=0")
				output, err := command.Output()
				if err != nil {
					return nil, err
				}
				return output, nil
			}
			i.run = func(name string, args ...string) error {
				_, err := i.query(name, args...)
				return err
			}
			for range 2 {
				if err := i.install(host); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := i.remove(host); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNativeCodexChangedHomeAndExternalRemoval(t *testing.T) {
	if os.Getenv("SERVEDIFF_TEST_NATIVE_HOSTS") != "1" {
		t.Skip("set SERVEDIFF_TEST_NATIVE_HOSTS=1 to exercise installed Codex CLI")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex unavailable")
	}
	i, _ := testInstaller(t)
	configuration := filepath.Join(i.home, "old codex")
	i.query = func(name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, name, args...)
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "CODEX_HOME=") {
				command.Env = append(command.Env, value)
			}
		}
		command.Env = append(command.Env, "CODEX_HOME="+configuration)
		return command.Output()
	}
	i.run = func(name string, args ...string) error {
		_, err := i.query(name, args...)
		return err
	}
	for _, name := range []string{"old codex", "new codex"} {
		configuration = filepath.Join(i.home, name)
		if err := os.MkdirAll(configuration, 0700); err != nil {
			t.Fatal(err)
		}
		if err := i.install("codex"); err != nil {
			t.Fatalf("install in %s: %v", name, err)
		}
		if registered, err := i.codexRegistered(i.root("codex")); err != nil || !registered {
			t.Fatalf("registration missing in %s: %v", name, err)
		}
	}
	if err := i.run("codex", "plugin", "marketplace", "remove", marketplace); err != nil {
		t.Fatal(err)
	}
	if err := i.install("codex"); err != nil {
		t.Fatalf("repair external removal: %v", err)
	}
	if err := i.remove("codex"); err != nil {
		t.Fatal(err)
	}
	if registered, err := i.codexRegistered(i.root("codex")); err != nil || registered {
		t.Fatalf("marketplace remains: %v", err)
	}
}

func TestInstalledAdaptersPreserveEnvironmentAndLaunchConfiguredCollector(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	source := os.Getenv("SERVEDIFF_TEST_PLUGIN_SOURCE")
	if source == "" {
		source, err = filepath.Abs("../..")
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []string{"opencode", "pi"} {
		t.Run(host, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(source, "packages", "plugin-"+host, "dist", "index.js")); err != nil {
				t.Skip("build adapters with mise run plugins:build first")
			}
			i, _ := testInstaller(t)
			i.source = source
			capture := filepath.Join(i.home, "collector arguments.json")
			captureJSON, _ := json.Marshal(capture)
			put(t, i.binary, "#!/usr/bin/env node\nrequire('node:fs').writeFileSync("+string(captureJSON)+", JSON.stringify(process.argv.slice(2)));\n")
			if err := os.Chmod(i.binary, 0700); err != nil {
				t.Fatal(err)
			}
			if err := i.install(host); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(i.root(host), "dist", "index.js")
			if host == "opencode" {
				entry = i.openCodeFile()
			}
			script := `import assert from 'node:assert/strict';
import {pathToFileURL} from 'node:url';
import {readFile} from 'node:fs/promises';
import {setTimeout} from 'node:timers/promises';
const before = {...process.env};
const plugin = await import(pathToFileURL(process.argv[1]));
assert.deepEqual({...process.env}, before);
if (process.argv[2] === 'pi') {
  plugin.default({on(_event, handler) {handler({}, {cwd:'/checkout with spaces', sessionManager:{getSessionId:()=>'session-id'}});}});
} else {
  async function* events() {yield {type:'session.status', data:{sessionID:'session-id',status:{type:'idle'}}};}
  const cleanup = plugin.default.setup({location:{directory:'/checkout with spaces'},event:{subscribe(){return events();}}});
  await setTimeout(20); cleanup();
}
assert.deepEqual({...process.env}, before);
for(let attempt=0; attempt<100; attempt++) {try {await readFile(process.argv[3]); break;} catch {await setTimeout(20);}}
`
			command := exec.Command(node, "--input-type=module", "-e", script, entry, host, capture)
			command.Dir = i.home
			command.Env = append(os.Environ(), "SERVEDIFF_BINARY=/unrelated/binary", "SERVEDIFF_CONFIG_PATH=/unrelated/config.json")
			if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
				t.Fatalf("installed module: %v %s", err, output)
			}
			var arguments []string
			if err := json.Unmarshal([]byte(read(t, capture)), &arguments); err != nil {
				t.Fatal(err)
			}
			expected := []string{"hook", "--agent", host, "--path", "/checkout with spaces", "--run-id", "session-id", "--config-file", i.configFile}
			if !reflect.DeepEqual(arguments, expected) {
				t.Fatalf("configured launch: %v", arguments)
			}
		})
	}
}
