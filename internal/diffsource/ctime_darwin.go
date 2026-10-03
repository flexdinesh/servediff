//go:build darwin

package diffsource

import (
	"os"
	"syscall"
)

func changedTime(_ string, info os.FileInfo) (float64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return float64(info.ModTime().UnixNano()) / 1_000_000, nil
	}
	return float64(stat.Ctimespec.Sec)*1_000 + float64(stat.Ctimespec.Nsec)/1_000_000, nil
}
