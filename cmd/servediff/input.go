package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
)

type loadedInput struct {
	source    diffsource.Source
	directory string
	mode      string
	snapshot  review.RepositoryDiff
	processed time.Duration
}

func loadInput(ctx context.Context, values options, stdin *os.File) (loadedInput, error) {
	started := time.Now()
	absolute, err := filepath.Abs(".")
	if err != nil {
		return loadedInput{}, err
	}
	var source diffsource.Source
	if values.fixture != "" {
		raw, readError := os.ReadFile(values.fixture)
		if readError != nil {
			return loadedInput{}, readError
		}
		source, err = diffsource.OpenPatch(string(raw))
	} else {
		piped, statError := redirected(stdin)
		if statError != nil {
			return loadedInput{}, statError
		}
		if values.directory == "-" || piped {
			raw, readError := io.ReadAll(io.LimitReader(stdin, diffsource.MaxInputBytes+1))
			if readError != nil {
				return loadedInput{}, readError
			}
			if len(raw) > diffsource.MaxInputBytes {
				return loadedInput{}, errors.New("piped diff exceeds the 16 MiB input limit")
			}
			source, err = diffsource.OpenPatch(string(raw))
		} else {
			if !values.repositorySet {
				return loadedInput{}, errors.New("provide a repository path, a fixture, or pipe a Git diff")
			}
			source, err = diffsource.OpenRepository(ctx, values.directory)
		}
	}
	if err != nil {
		return loadedInput{}, err
	}
	mode := "pipe"
	if source.Kind() == "local" {
		mode = "git"
		absolute = source.Root()
	}
	snapshot, err := source.Snapshot(ctx, review.DiffAll)
	if err != nil {
		return loadedInput{}, err
	}
	return loadedInput{source: source, directory: absolute, mode: mode, snapshot: snapshot, processed: time.Since(started)}, nil
}

func redirected(stdin *os.File) (bool, error) {
	stat, err := stdin.Stat()
	if err != nil {
		return false, err
	}
	return stat.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0 || stat.Mode().IsRegular(), nil
}
