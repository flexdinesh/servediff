package reviewstore

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testUser(t *testing.T, store *Store, uid string) User {
	t.Helper()
	user, err := store.User(uid, uid)
	if err != nil {
		t.Fatal(err)
	}
	return user
}
