package reviewstore

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSchemaResetOnceAndFutureVersionRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE legacy(value TEXT); INSERT INTO legacy VALUES('discard'); PRAGMA user_version=8")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err := store.db.QueryRow("SELECT name FROM sqlite_master WHERE name='legacy'").Scan(&name); err != sql.ErrNoRows {
		t.Fatalf("legacy table survived: %v", err)
	}
	user, err := store.User("owner", "owner")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.User("owner", "owner")
	if err != nil || retained.ID != user.ID {
		t.Fatalf("current schema reset on reopen: %+v %v", retained, err)
	}
	if _, err := store.db.Exec("PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if newer, err := Open(path); err == nil {
		newer.Close()
		t.Fatal("accepted future schema")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("future state changed: %d %v", count, err)
	}
}
