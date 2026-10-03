package diffsource

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FILE_BASIC_INFO includes metadata change time, which os.FileInfo omits.
type fileBasicInfo struct {
	creationTime   int64
	lastAccessTime int64
	lastWriteTime  int64
	changeTime     int64
	attributes     uint32
	_              uint32
}

func changedTime(path string, _ os.FileInfo) (float64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	var info fileBasicInfo
	err = windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	err = errors.Join(err, windows.CloseHandle(handle))
	// FILETIME uses 100-nanosecond ticks since 1601; fingerprints use Unix ms.
	return float64(info.changeTime-116444736000000000) / 10000, err
}
