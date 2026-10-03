// Package processlock provides kernel-owned process locks. Lock files persist;
// removing them would let another process lock a different inode at the same path.
package processlock

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var ErrLocked = errors.New("process lock held")

type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

func Acquire(path string) (*Lock, error)    { return acquire(path, false) }
func TryAcquire(path string) (*Lock, error) { return acquire(path, true) }

func acquire(path string, nonblocking bool) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file, nonblocking); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Lock{file: file}, nil
}

func (lock *Lock) Close() error {
	lock.once.Do(func() { lock.err = errors.Join(unlockFile(lock.file), lock.file.Close()) })
	return lock.err
}
