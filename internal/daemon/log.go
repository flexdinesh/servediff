package daemon

import (
	"errors"
	"io"
	"os"
	"sync"
)

const logLimit = 2 << 20

type logWriter struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	size int64
}

func rotateLog(dir string) error {
	info, err := os.Stat(LogPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < logLimit {
		return nil
	}
	_ = os.Remove(LogPath(dir) + ".1")
	return os.Rename(LogPath(dir), LogPath(dir)+".1")
}

func OpenLog(dir string) (io.WriteCloser, error) {
	if err := rotateLog(dir); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(LogPath(dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &logWriter{dir: dir, file: file, size: info.Size()}, nil
}

func (writer *logWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	originalLength := len(data)
	if len(data) > logLimit {
		data = data[len(data)-logLimit:]
	}
	if writer.size+int64(len(data)) > logLimit {
		if err := writer.file.Close(); err != nil {
			return 0, err
		}
		_ = os.Remove(LogPath(writer.dir) + ".1")
		if err := os.Rename(LogPath(writer.dir), LogPath(writer.dir)+".1"); err != nil {
			return 0, err
		}
		file, err := os.OpenFile(LogPath(writer.dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return 0, err
		}
		writer.file, writer.size = file, 0
	}
	count, err := writer.file.Write(data)
	writer.size += int64(count)
	if err == nil && count == len(data) {
		return originalLength, nil
	}
	return count, err
}

func (writer *logWriter) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.file.Close()
}
