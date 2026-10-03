//go:build !windows

package diffsource

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Each invocation owns a process group, including Git helpers and hooks.
func runGitCommand(_ context.Context, command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = time.Second
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	var copyCleanupErr error
	var cleanupMutex sync.Mutex
	for _, writer := range []*io.Writer{&command.Stdout, &command.Stderr} {
		if *writer != nil {
			*writer = &gitOutputWriter{Writer: *writer, failed: func() {
				err := command.Cancel()
				if errors.Is(err, os.ErrProcessDone) {
					return
				}
				cleanupMutex.Lock()
				copyCleanupErr = errors.Join(copyCleanupErr, err)
				cleanupMutex.Unlock()
			}}
		}
	}
	if err := command.Start(); err != nil {
		return err
	}
	err := command.Wait()
	// Even a successful leader may leave helpers running after closing output.
	cleanupErr := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(cleanupErr, syscall.ESRCH) {
		cleanupErr = nil
	}
	cleanupMutex.Lock()
	defer cleanupMutex.Unlock()
	return errors.Join(err, cleanupErr, copyCleanupErr)
}

// Output-limit errors must terminate a writer that keeps producing output.
type gitOutputWriter struct {
	io.Writer
	failed func()
}

func (writer *gitOutputWriter) Write(value []byte) (int, error) {
	n, err := writer.Writer.Write(value)
	if err != nil {
		writer.failed()
	}
	return n, err
}
