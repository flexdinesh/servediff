//go:build linux

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
	return float64(stat.Ctim.Sec)*1_000 + float64(stat.Ctim.Nsec)/1_000_000, nil
}
