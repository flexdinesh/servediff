package contextservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/flexdinesh/servediff/internal/diffsource"
	"github.com/flexdinesh/servediff/internal/review"
	"github.com/flexdinesh/servediff/internal/reviewstore"
)

const (
	maxCachedSources   = 128
	maxSourceBytes     = 64 * 1024 * 1024
	sourceIdleLifetime = 10 * time.Minute
	unavailableRetry   = time.Second
)

type fileIdentity struct {
	path     string
	info     os.FileInfo
	optional bool
	routing  bool
}

type cachedSource struct {
	source           diffsource.Source
	checks           []fileIdentity
	bytes            int
	lastUsed         time.Time
	unavailableUntil time.Time
}

type sourceLoad struct {
	done       chan struct{}
	source     diffsource.Source
	err        error
	generation uint64
}

func newCaptureCache(source diffsource.Source, raw string, snapshot review.RepositoryDiff) *cachedSource {
	// Account original/normalized patches, preview strings and file metadata.
	// Active requests and parser allocations remain outside this retained-data budget.
	return &cachedSource{source: source, bytes: 3*len(raw) + 512*len(snapshot.Files), lastUsed: time.Now()}
}

func newWorktreeCache(source diffsource.Source, commonDir, worktreeDir string) *cachedSource {
	entry := &cachedSource{source: source, lastUsed: time.Now()}
	for _, path := range []string{source.Root(), commonDir, worktreeDir, filepath.Join(source.Root(), ".git"), filepath.Join(worktreeDir, "commondir")} {
		info, err := os.Stat(path)
		entry.checks = append(entry.checks, fileIdentity{path: path, info: info, optional: errors.Is(err, os.ErrNotExist), routing: info != nil && info.Mode().IsRegular()})
	}
	return entry
}

func (entry *cachedSource) valid() (bool, bool) {
	for _, check := range entry.checks {
		info, err := os.Stat(check.path)
		if check.optional && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || check.info == nil {
			return false, errors.Is(err, os.ErrNotExist) && !check.routing && !check.optional
		}
		if !os.SameFile(info, check.info) {
			// Replaced directories must be explicitly registered again before
			// their new data can inherit an existing review context.
			return false, !check.routing
		}
		if check.routing && (info.Size() != check.info.Size() || !info.ModTime().Equal(check.info.ModTime())) {
			return false, false
		}
	}
	return true, false
}

func (service *Service) invalidateSource(id string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if entry := service.sources[id]; entry != nil {
		service.sourceBytes -= entry.bytes
		delete(service.sources, id)
	}
	service.generation[id]++
	delete(service.identityChanged, id)
}

func (service *Service) cacheSource(id string, entry *cachedSource, generation uint64) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if generation != 0 && generation != service.generation[id] {
		return
	}
	service.cacheSourceLocked(id, entry)
}

func (service *Service) cacheSourceLocked(id string, entry *cachedSource) {
	if old := service.sources[id]; old != nil {
		service.sourceBytes -= old.bytes
		delete(service.sources, id)
	}
	now := time.Now()
	for key, old := range service.sources {
		if now.Sub(old.lastUsed) > sourceIdleLifetime {
			service.sourceBytes -= old.bytes
			delete(service.sources, key)
		}
	}
	if entry.bytes > maxSourceBytes {
		return
	}
	for len(service.sources) >= maxCachedSources || service.sourceBytes+entry.bytes > maxSourceBytes {
		var oldestID string
		var oldest *cachedSource
		for key, entry := range service.sources {
			if oldest == nil || entry.lastUsed.Before(oldest.lastUsed) {
				oldestID, oldest = key, entry
			}
		}
		if oldest == nil {
			break
		}
		service.sourceBytes -= oldest.bytes
		delete(service.sources, oldestID)
	}
	service.sources[id] = entry
	service.sourceBytes += entry.bytes
}

func (service *Service) cached(id string) (diffsource.Source, bool) {
	service.mu.Lock()
	entry := service.sources[id]
	if entry == nil {
		service.mu.Unlock()
		return nil, false
	}
	now := time.Now()
	if now.Sub(entry.lastUsed) > sourceIdleLifetime || (!entry.unavailableUntil.IsZero() && !now.Before(entry.unavailableUntil)) {
		service.sourceBytes -= entry.bytes
		delete(service.sources, id)
		service.mu.Unlock()
		return nil, false
	}
	entry.lastUsed = now
	service.mu.Unlock()
	if !entry.unavailableUntil.IsZero() {
		return entry.source, true
	}
	valid, changedIdentity := entry.valid()
	if valid {
		return entry.source, true
	}
	service.mu.Lock()
	if service.sources[id] == entry {
		service.sourceBytes -= entry.bytes
		delete(service.sources, id)
		if changedIdentity {
			service.identityChanged[id] = true
		}
	}
	service.mu.Unlock()
	return nil, false
}

func (service *Service) sourceFor(ctx context.Context, item reviewstore.ContextInfo) (diffsource.Source, error) {
	if err := service.background.Err(); err != nil {
		return nil, err
	}
	if source, ok := service.cached(item.ID); ok {
		if err := service.background.Err(); err != nil {
			return nil, err
		}
		return source, nil
	}
	service.mu.Lock()
	if service.closing || service.background.Err() != nil {
		service.mu.Unlock()
		return nil, context.Canceled
	}
	load := service.loading[item.ID]
	if load == nil {
		load = &sourceLoad{done: make(chan struct{}), generation: service.generation[item.ID]}
		service.loading[item.ID] = load
		identityChanged := service.identityChanged[item.ID]
		service.loaders.Add(1)
		go service.loadSource(item, load, identityChanged)
	}
	service.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-load.done:
		return load.source, load.err
	}
}

func (service *Service) loadSource(item reviewstore.ContextInfo, load *sourceLoad, identityChanged bool) {
	defer service.loaders.Done()
	ctx, cancel := context.WithTimeout(service.background, 30*time.Second)
	defer cancel()
	var entry *cachedSource
	var source diffsource.Source
	var err error
	if item.Kind == "capture" {
		select {
		case service.parseSlots <- struct{}{}:
			defer func() { <-service.parseSlots }()
		default:
			service.mu.Lock()
			load.err = diffsource.Error(503, "Capture parsing is busy; retry shortly")
			if service.loading[item.ID] == load {
				delete(service.loading, item.ID)
			}
			close(load.done)
			service.mu.Unlock()
			return
		}
		var raw string
		raw, _, err = service.store.ReopenCapture(service.user.ID, item.ID, time.Now())
		if err != nil {
			err = requestError(err)
		} else {
			source, err = diffsource.OpenPatch(raw)
			if err == nil {
				var snapshot review.RepositoryDiff
				snapshot, err = source.Snapshot(ctx, review.DiffAll)
				if err == nil {
					entry = newCaptureCache(source, raw, snapshot)
				}
			}
		}
	} else if item.Root == nil || item.CommonDir == nil || item.WorktreeKey == nil {
		err = errors.New("incomplete worktree identity")
	} else {
		if identityChanged {
			err = errors.New("registered worktree directory replaced")
		} else {
			source, err = diffsource.OpenRepository(ctx, *item.Root)
			if err == nil {
				var commonDir, worktreeKey string
				commonDir, worktreeKey, err = diffsource.RepositoryIdentity(ctx, source.Root())
				if err == nil && (commonDir != *item.CommonDir || worktreeKey != *item.WorktreeKey) {
					err = errors.New("registered worktree identity changed")
				}
				if err == nil {
					entry = newWorktreeCache(source, commonDir, worktreeKey)
				}
			}
		}
		if err != nil && ctx.Err() == nil {
			source = &unavailableSource{root: *item.Root}
			entry = &cachedSource{source: source, lastUsed: time.Now(), unavailableUntil: time.Now().Add(unavailableRetry)}
			err = nil
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
		entry = nil
	}
	service.mu.Lock()
	if !service.closing && load.generation == service.generation[item.ID] && entry != nil {
		service.cacheSourceLocked(item.ID, entry)
		if item.Kind == "worktree" {
			availability := "available"
			if !entry.unavailableUntil.IsZero() {
				availability = "unavailable"
			}
			service.availability[item.ID] = availability
		}
	}
	load.source, load.err = source, err
	if service.loading[item.ID] == load {
		delete(service.loading, item.ID)
	}
	close(load.done)
	service.mu.Unlock()
}
