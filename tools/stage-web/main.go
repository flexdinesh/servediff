package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: stage-web source destination")
		os.Exit(2)
	}
	if err := stage(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func stage(source, destination string) error {
	if _, err := os.Stat(filepath.Join(source, "index.html")); err != nil {
		return fmt.Errorf("web build missing: %w", err)
	}
	marker, err := os.Stat(filepath.Join(destination, "placeholder.txt"))
	if err != nil || !marker.Mode().IsRegular() {
		return errors.New("refusing to replace an unmarked web asset directory")
	}
	entries, err := os.ReadDir(destination)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "placeholder.txt" {
			if err := os.RemoveAll(filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.Create(target)
		if err != nil {
			return err
		}
		_, copyError := io.Copy(output, input)
		closeError := output.Close()
		return errors.Join(copyError, closeError)
	})
}
