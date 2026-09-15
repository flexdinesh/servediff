package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maximumShimSize = 64 * 1024

func removeLegacyShims(directory string) ([]string, error) {
	removed := make([]string, 0, 3)
	for _, name := range []string{"servediff", "servediff.cmd", "servediff.ps1"} {
		path := filepath.Join(directory, name)
		stat, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if stat.Size() > maximumShimSize {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		contents := strings.ReplaceAll(string(raw), `\`, "/")
		if !strings.Contains(contents, "servediff-workspace") || !strings.Contains(contents, "apps/server/src/cli.ts") {
			continue
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		removed = append(removed, path)
	}
	return removed, nil
}

func run() error {
	output, err := exec.Command("pnpm", "bin", "--global").Output()
	if err != nil {
		return fmt.Errorf("locate pnpm global bin: %w", err)
	}
	removed, err := removeLegacyShims(strings.TrimSpace(string(output)))
	if err != nil {
		return err
	}
	for _, path := range removed {
		fmt.Printf("  removed obsolete Node shim: %s\n", path)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "  remove legacy servediff install: %v\n", err)
		os.Exit(1)
	}
}
