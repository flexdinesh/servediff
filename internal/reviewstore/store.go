package reviewstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/flexdinesh/servediff/internal/review"
)

type sessionReview struct {
	Comments []review.ReviewComment `json:"comments"`
	Marks    []review.ReviewMark    `json:"marks"`
}

type reviewData struct {
	Version  int                      `json:"version"`
	Sessions map[string]sessionReview `json:"sessions"`
}

type Store struct {
	mu   sync.Mutex
	path string
	data reviewData
}

func DefaultPath(sessionID string) (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "servediff", "reviews", sessionID+".json"), nil
}

func Open(path string) (*Store, error) {
	store := &Store{path: path, data: reviewData{Version: 1, Sessions: make(map[string]sessionReview)}}
	if path == "" {
		return store, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	data, err := parseData(raw)
	if err != nil {
		return nil, errors.New("invalid servediff review data")
	}
	store.data = data
	return store, nil
}

func parseData(raw []byte) (reviewData, error) {
	var envelope struct {
		Version  int                        `json:"version"`
		Sessions map[string]json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Version != 1 || envelope.Sessions == nil {
		return reviewData{}, errors.New("unsupported review data")
	}
	data := reviewData{Version: 1, Sessions: make(map[string]sessionReview)}
	for key, rawSession := range envelope.Sessions {
		var session struct {
			Comments []json.RawMessage `json:"comments"`
			Marks    []json.RawMessage `json:"marks"`
		}
		if json.Unmarshal(rawSession, &session) != nil {
			continue
		}
		parsed := sessionReview{}
		for _, rawComment := range session.Comments {
			var comment review.ReviewComment
			if json.Unmarshal(rawComment, &comment) == nil && comment.Valid() {
				parsed.Comments = append(parsed.Comments, comment)
			}
		}
		for _, rawMark := range session.Marks {
			var mark review.ReviewMark
			if json.Unmarshal(rawMark, &mark) == nil {
				if _, err := review.ParseDiffMode(string(mark.Scope)); err == nil {
					parsed.Marks = append(parsed.Marks, mark)
				}
			}
		}
		data.Sessions[key] = parsed
	}
	return data, nil
}

func (store *Store) session(id string) sessionReview {
	return store.data.Sessions[id]
}

func (store *Store) save() error {
	if store.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(store.data)
	if err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.%d.tmp", store.path, os.Getpid())
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, store.path)
}

func (store *Store) Comments(id string) []review.ReviewComment {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]review.ReviewComment(nil), store.session(id).Comments...)
}

func (store *Store) PutComment(id string, comment review.ReviewComment) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	session := store.session(id)
	found := false
	for index := range session.Comments {
		if session.Comments[index].ID == comment.ID {
			session.Comments[index] = comment
			found = true
			break
		}
	}
	if !found {
		session.Comments = append(session.Comments, comment)
	}
	store.data.Sessions[id] = session
	return store.save()
}

func (store *Store) ImportComments(id string, comments []review.ReviewComment) ([]review.ReviewComment, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	session := store.session(id)
	known := make(map[string]bool, len(session.Comments))
	for _, comment := range session.Comments {
		known[comment.ID] = true
	}
	for _, comment := range comments {
		if !known[comment.ID] {
			session.Comments = append(session.Comments, comment)
			known[comment.ID] = true
		}
	}
	store.data.Sessions[id] = session
	return append([]review.ReviewComment(nil), session.Comments...), store.save()
}

func (store *Store) DeleteComments(id string, selected map[string]bool) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	session := store.session(id)
	next := session.Comments[:0]
	for _, comment := range session.Comments {
		if !selected[comment.ID] {
			next = append(next, comment)
		}
	}
	deleted := len(session.Comments) - len(next)
	session.Comments = next
	store.data.Sessions[id] = session
	return deleted, store.save()
}

func (store *Store) DeleteComment(id, commentID string) (bool, error) {
	deleted, err := store.DeleteComments(id, map[string]bool{commentID: true})
	return deleted == 1, err
}

func (store *Store) Marks(id string, scope review.DiffMode) []review.ReviewMark {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]review.ReviewMark, 0)
	for _, mark := range store.session(id).Marks {
		if mark.Scope == scope {
			result = append(result, mark)
		}
	}
	return result
}

func (store *Store) PutMark(id string, mark review.ReviewMark) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	session := store.session(id)
	next := session.Marks[:0]
	for _, existing := range session.Marks {
		if existing.Scope != mark.Scope || existing.FileID != mark.FileID {
			next = append(next, existing)
		}
	}
	session.Marks = append(next, mark)
	store.data.Sessions[id] = session
	return store.save()
}

func (store *Store) DeleteMark(id string, scope review.DiffMode, fileID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	session := store.session(id)
	next := session.Marks[:0]
	for _, mark := range session.Marks {
		if mark.Scope != scope || mark.FileID != fileID {
			next = append(next, mark)
		}
	}
	session.Marks = next
	store.data.Sessions[id] = session
	return store.save()
}

func (store *Store) ClearMarks(id string, scope review.DiffMode) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	session := store.session(id)
	next := session.Marks[:0]
	for _, mark := range session.Marks {
		if mark.Scope != scope {
			next = append(next, mark)
		}
	}
	session.Marks = next
	store.data.Sessions[id] = session
	return store.save()
}
