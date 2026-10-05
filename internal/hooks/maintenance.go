package hooks

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/flexdinesh/servediff/internal/processlock"
)

const startupCooldown = time.Minute
const maxLogBytes = 128 << 10

// Start serializes startup attempts across checkouts and suppresses repeated
// failures. Call after a health probe fails; callback should probe again before
// starting, since another worker may have started the service while waiting.
func (engine Engine) Start(ctx context.Context, destination string, start func(context.Context) error) error {
	directory := filepath.Join(engine.Directory, "startup", key(destination))
	timeout := engine.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lock, err := startupLock(ctx, filepath.Join(directory, "startup.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	path := filepath.Join(directory, "failure.json")
	var failed time.Time
	if readJSON(path, &failed) == nil && time.Since(failed) < startupCooldown {
		return ErrCooldown
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := start(ctx); err != nil {
		_ = writeJSON(path, time.Now())
		return err
	}
	_ = os.Remove(path)
	return nil
}

func startupLock(ctx context.Context, path string) (*processlock.Lock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lock, err := processlock.TryAcquire(path)
		if !errors.Is(err, processlock.ErrLocked) {
			return lock, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Log never writes into the agent's conversation or inherits its output pipes.
func (engine Engine) Log(err error) {
	if err == nil {
		return
	}
	lock, lockErr := processlock.TryAcquire(filepath.Join(engine.Directory, "log.lock"))
	if lockErr != nil {
		return
	}
	defer lock.Close()
	path := filepath.Join(engine.Directory, "hooks.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() >= maxLogBytes {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	file, openErr := os.OpenFile(path, flags, 0o600)
	if openErr != nil {
		return
	}
	defer file.Close()
	message := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(message) > 4096 {
		message = message[:4096]
	}
	_, _ = fmt.Fprintf(file, "%s %s\n", time.Now().UTC().Format(time.RFC3339), message)
}

// Payloads are disposable, locks are not: unlinking a live lock creates two
// owners. Cleanup holds each idle worker's lock before removing bounded state.
func (engine Engine) prune() {
	retention, err := processlock.TryAcquire(filepath.Join(engine.Directory, "retention.lock"))
	if err != nil {
		return
	}
	defer retention.Close()
	type payload struct {
		path string
		size int64
		at   time.Time
	}
	var pending []payload
	var expired []payload
	var total int64
	_ = filepath.WalkDir(filepath.Join(engine.Directory, "jobs"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			file := payload{path, info.Size(), info.ModTime()}
			if strings.HasSuffix(path, ".pending.json") {
				pending = append(pending, file)
				total += info.Size()
			} else if time.Since(info.ModTime()) >= pendingTTL {
				expired = append(expired, file)
			}
		}
		return nil
	})
	sort.Slice(pending, func(i, j int) bool { return pending[i].at.Before(pending[j].at) })
	for _, file := range pending {
		if total <= maxPendingBytes && time.Since(file.at) < pendingTTL {
			continue
		}
		lock, err := processlock.TryAcquire(filepath.Join(filepath.Dir(file.path), "worker.lock"))
		if err != nil {
			continue
		}
		// A worker may have replaced its failed observation after the scan.
		if info, err := os.Stat(file.path); err == nil {
			total += info.Size() - file.size
			if (total > maxPendingBytes || time.Since(info.ModTime()) >= pendingTTL) && os.Remove(file.path) == nil {
				total -= info.Size()
			}
		}
		_ = lock.Close()
	}
	for _, file := range expired {
		directory := filepath.Dir(file.path)
		worker, err := processlock.TryAcquire(filepath.Join(directory, "worker.lock"))
		if err != nil {
			continue
		}
		queue, err := processlock.TryAcquire(filepath.Join(directory, "queue.lock"))
		if err == nil {
			// A scheduler may have replaced an old request during the scan.
			if info, err := os.Stat(file.path); err == nil && time.Since(info.ModTime()) >= pendingTTL {
				_ = os.Remove(file.path)
			}
			_ = queue.Close()
		}
		_ = worker.Close()
	}
}
