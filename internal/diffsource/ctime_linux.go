//go:build linux

package diffsource

import (
	"os"
	"syscall"
)

func changedTime(info os.FileInfo) float64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return float64(info.ModTime().UnixNano()) / 1_000_000
	}
	return float64(stat.Ctim.Sec)*1_000 + float64(stat.Ctim.Nsec)/1_000_000
}
