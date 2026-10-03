//go:build !windows

package daemon

import (
	"errors"
	"os"
	"syscall"
)

func privateInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0o077 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("daemon runtime must be private and owned by this user")
	}
	return info, nil
}

func protectRuntime(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("daemon runtime must be a directory owned by this user")
	}
	return os.Chmod(path, 0o700)
}

func validatePrivateFile(path string) error {
	info, err := privateInfo(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("invalid daemon descriptor file")
	}
	return nil
}
