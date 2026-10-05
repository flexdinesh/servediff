// Install local agent adapters without editing unrelated host configuration.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const ownership = ".servediff-local-install"
const marker = "servediff local plugin v1\n"
const jsMarker = "// servediff managed local plugin\n"
const marketplace = "servediff-local"

type installer struct {
	source, binary, configFile, home, data, config string
	env                                            func(string) string
	run                                            func(string, ...string) error
	query                                          func(string, ...string) ([]byte, error)
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 || (args[0] != "install" && args[0] != "remove") {
		return errors.New("usage: install-plugins install|remove --host codex|claude|opencode|pi|all [--source DIR] [--binary FILE] [--config-file FILE]")
	}
	flags := flag.NewFlagSet("install-plugins "+args[0], flag.ContinueOnError)
	flags.SetOutput(output)
	host := flags.String("host", "all", "agent host, or all")
	source := flags.String("source", ".", "servediff repository containing built plugin packages")
	binary := flags.String("binary", "dist/servediff", "servediff executable; captured as an absolute path")
	configFile := flags.String("config-file", "", "optional machine JSON config path")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments; use --host NAME")
	}
	hosts := []string{*host}
	if *host == "all" {
		hosts = []string{"codex", "claude", "opencode", "pi"}
	}
	for _, name := range hosts {
		if name != "codex" && name != "claude" && name != "opencode" && name != "pi" {
			return fmt.Errorf("unknown host %q", name)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	i := installer{home: home, env: os.Getenv, run: runHost, query: queryHost}
	i.data = i.location("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	i.config = i.location("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	i.source, err = filepath.Abs(*source)
	if err != nil {
		return err
	}
	if args[0] == "install" {
		i.binary, err = filepath.Abs(*binary)
		if err != nil {
			return err
		}
		stat, err := os.Stat(i.binary)
		if err != nil || !stat.Mode().IsRegular() {
			return fmt.Errorf("servediff binary unavailable at %s; run mise run build first", i.binary)
		}
		if runtime.GOOS != "windows" && stat.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("servediff binary is not executable: %s", i.binary)
		}
		if *configFile != "" {
			i.configFile, err = filepath.Abs(*configFile)
			if err != nil {
				return err
			}
		}
	}
	var failures []error
	for _, name := range hosts {
		var err error
		if args[0] == "install" {
			err = i.install(name)
		} else {
			err = i.remove(name)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
			continue
		}
		fmt.Fprintf(output, "%s: %s complete\n", name, args[0])
		if name == "codex" && args[0] == "install" {
			fmt.Fprintln(output, "Codex: enable hooks and trust the servediff plugin when prompted; restart sessions.")
		}
	}
	return errors.Join(failures...)
}

func runHost(name string, args ...string) error {
	_, err := queryHost(name, args...)
	return err
}

func queryHost(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// Do not inherit stdin: installation must never start an interactive agent.
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			output = append(output, exit.Stderr...)
		}
		text := strings.TrimSpace(string(output))
		if len(text) > 2048 {
			text = text[:2048]
		}
		return nil, fmt.Errorf("%s %s: %w %s", name, strings.Join(args, " "), err, text)
	}
	return output, nil
}

func (i installer) location(key, fallback string) string {
	if value := i.env(key); value != "" {
		return value
	}
	return fallback
}

func (i installer) root(host string) string {
	if host == "claude" {
		return filepath.Join(i.location("CLAUDE_CONFIG_DIR", filepath.Join(i.home, ".claude")), "skills", "servediff")
	}
	return filepath.Join(i.data, "servediff", "plugins", host)
}

func (i installer) openCodeFile() string {
	return filepath.Join(i.config, "opencode", "plugins", "servediff.js")
}

func owned(root string) (bool, error) {
	stat, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !stat.IsDir() {
		return false, fmt.Errorf("refusing unmanaged path %s", root)
	}
	contents, err := os.ReadFile(filepath.Join(root, ownership))
	if err != nil || string(contents) != marker {
		return false, fmt.Errorf("refusing unmanaged directory %s", root)
	}
	return true, nil
}

func (i installer) install(host string) error {
	root := i.root(host)
	previous, err := owned(root)
	if err != nil {
		return err
	}
	registered := false
	if host == "codex" {
		registered, err = i.codexRegistered(root)
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(root), ".servediff-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	destination := temporary
	if host == "codex" {
		destination = filepath.Join(temporary, "plugins", "servediff")
	}
	if err := copyPackage(filepath.Join(i.source, "packages", "plugin-"+host), destination); err != nil {
		return err
	}
	if host == "codex" || host == "claude" {
		if err := i.rewriteHook(destination, host); err != nil {
			return err
		}
	} else {
		entry := filepath.Join(destination, "dist", "index.js")
		contents, err := os.ReadFile(entry)
		if err != nil {
			return fmt.Errorf("built plugin missing; build plugin packages first: %w", err)
		}
		installed := append([]byte(jsMarker), contents...)
		installed = append(installed, []byte(i.jsConfiguration())...)
		if err := os.WriteFile(entry, installed, 0600); err != nil {
			return err
		}
	}
	if host == "codex" {
		catalog := map[string]any{"name": marketplace, "plugins": []any{map[string]any{
			"name": "servediff", "source": map[string]string{"source": "local", "path": "./plugins/servediff"},
			"policy": map[string]string{"installation": "AVAILABLE", "authentication": "ON_INSTALL"}, "category": "Productivity",
		}}}
		if err := writeJSON(filepath.Join(temporary, ".agents", "plugins", "marketplace.json"), catalog); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(temporary, ownership), []byte(marker), 0600); err != nil {
		return err
	}
	if err := replaceDirectory(temporary, root, previous); err != nil {
		return err
	}
	switch host {
	case "codex":
		if !registered {
			if err := i.run("codex", "plugin", "marketplace", "add", root); err != nil {
				return err
			}
		}
		return i.run("codex", "plugin", "add", "servediff@"+marketplace)
	case "opencode":
		contents, err := os.ReadFile(filepath.Join(root, "dist", "index.js"))
		if err != nil {
			return err
		}
		file := i.openCodeFile()
		if existing, err := os.ReadFile(file); err == nil && !strings.HasPrefix(string(existing), jsMarker) {
			return fmt.Errorf("refusing unmanaged plugin %s", file)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			return err
		}
		return os.WriteFile(file, contents, 0600)
	case "pi":
		return i.run("pi", "install", root)
	}
	return nil
}

func replaceDirectory(temporary, root string, previous bool) error {
	backup := root + ".previous"
	if previous {
		if _, err := os.Lstat(backup); !os.IsNotExist(err) {
			return fmt.Errorf("previous installation backup exists: %s", backup)
		}
		if err := os.Rename(root, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(temporary, root); err != nil {
		if previous {
			_ = os.Rename(backup, root)
		}
		return err
	}
	if previous {
		return os.RemoveAll(backup)
	}
	return nil
}

func copyPackage(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "node_modules" || entry.Name() == ".git" {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported plugin file %s", path)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, contents, stat.Mode().Perm())
	})
}

func (i installer) jsConfiguration() string {
	settings := struct {
		Binary     string `json:"binary"`
		ConfigFile string `json:"configFile,omitempty"`
	}{i.binary, i.configFile}
	configuration, _ := json.Marshal(settings)
	return "\nconfigure(" + string(configuration) + ");\n"
}

func (i installer) codexRegistered(root string) (bool, error) {
	output, err := i.query("codex", "plugin", "marketplace", "list", "--json")
	if err != nil {
		return false, err
	}
	var document struct {
		Marketplaces *[]struct {
			Name              string `json:"name"`
			Root              string `json:"root"`
			MarketplaceSource struct {
				SourceType string `json:"sourceType"`
			} `json:"marketplaceSource"`
		} `json:"marketplaces"`
	}
	if err := json.Unmarshal(output, &document); err != nil {
		return false, fmt.Errorf("read Codex marketplaces: %w", err)
	}
	if document.Marketplaces == nil {
		return false, errors.New("Codex marketplace list omitted marketplaces")
	}
	for _, entry := range *document.Marketplaces {
		if entry.Name != marketplace {
			continue
		}
		if entry.MarketplaceSource.SourceType != "local" || !samePath(entry.Root, root) {
			return false, fmt.Errorf("refusing unrelated Codex marketplace %s at %s", marketplace, entry.Root)
		}
		return true, nil
	}
	return false, nil
}

func samePath(first, second string) bool {
	if resolved, err := filepath.EvalSymlinks(first); err == nil {
		first = resolved
	}
	if resolved, err := filepath.EvalSymlinks(second); err == nil {
		second = resolved
	}
	return filepath.Clean(first) == filepath.Clean(second)
}

func (i installer) rewriteHook(root, host string) error {
	file := filepath.Join(root, "hooks", "hooks.json")
	contents, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		return err
	}
	command := quoteCommand(i.binary) + " hook --agent " + host
	if i.configFile != "" {
		command += " --config-file " + quoteCommand(i.configFile)
	}
	if runtime.GOOS == "windows" {
		command += " >nul 2>&1 & exit /b 0"
	} else {
		command += " >/dev/null 2>&1 || true"
	}
	var rewrite func(any)
	found := false
	rewrite = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if _, ok := value["command"].(string); ok {
				value["command"] = command
				found = true
			}
			for _, child := range value {
				rewrite(child)
			}
		case []any:
			for _, child := range value {
				rewrite(child)
			}
		}
	}
	rewrite(document)
	if !found {
		return errors.New("plugin has no command hook")
	}
	return writeJSON(file, document)
}

func quoteCommand(value string) string {
	if runtime.GOOS == "windows" {
		// Native Windows hook hosts invoke commands using cmd.exe.
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeJSON(path string, value any) error {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, append(contents, '\n'), 0600)
}

func (i installer) remove(host string) error {
	root := i.root(host)
	exists, err := owned(root)
	if err != nil || !exists {
		return err
	}
	switch host {
	case "codex":
		registered, err := i.codexRegistered(root)
		if err != nil {
			return err
		}
		if registered {
			if err := i.run("codex", "plugin", "remove", "servediff@"+marketplace); err != nil {
				return err
			}
			if err := i.run("codex", "plugin", "marketplace", "remove", marketplace); err != nil {
				return err
			}
		}
	case "pi":
		if err := i.run("pi", "remove", root); err != nil {
			return err
		}
	case "opencode":
		file := i.openCodeFile()
		contents, err := os.ReadFile(file)
		if err == nil {
			if !strings.HasPrefix(string(contents), jsMarker) {
				return fmt.Errorf("refusing unmanaged plugin %s", file)
			}
			if err := os.Remove(file); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return os.RemoveAll(root)
}
