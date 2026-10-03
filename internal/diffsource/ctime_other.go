//go:build !linux && !darwin && !windows

package diffsource

import "os"

func changedTime(_ string, info os.FileInfo) (float64, error) {
	return float64(info.ModTime().UnixNano()) / 1_000_000, nil
}
