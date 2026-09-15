//go:build !linux && !darwin

package diffsource

import "os"

func changedTime(info os.FileInfo) float64 {
	return float64(info.ModTime().UnixNano()) / 1_000_000
}
