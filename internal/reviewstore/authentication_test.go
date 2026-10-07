package reviewstore

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteCredentialsPersistAndNeverOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 40)
	admin, err := store.EnsureRemoteUser("admin", token)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.ProvisionUser("other", strings.Repeat("b", 40))
	if err != nil || other.ID == admin.ID {
		t.Fatalf("provision second user: %+v %v", other, err)
	}
	if _, err := store.ProvisionUser("other", strings.Repeat("c", 40)); err == nil {
		t.Fatal("overwrote existing user")
	}
	if _, err := store.ProvisionUser("duplicate", token); err == nil {
		t.Fatal("shared credential across users")
	}
	var stored string
	if err := store.db.QueryRow(`SELECT token_hash FROM remote_credentials WHERE user_id=?`, admin.ID).Scan(&stored); err != nil || stored == token || stored != tokenHash(token) {
		t.Fatalf("credential was not hashed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	again, err := store.EnsureRemoteUser("admin", token)
	if err != nil || again != admin {
		t.Fatalf("restart changed identity: %+v %v", again, err)
	}
	if _, err := store.EnsureRemoteUser("admin", strings.Repeat("d", 40)); err == nil {
		t.Fatal("restart replaced credential")
	}
	user, err := store.AuthenticateToken(token)
	if err != nil || user != admin {
		t.Fatalf("persisted token lost: %+v %v", user, err)
	}
	if _, err := store.AuthenticateToken("wrong"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("invalid token: %v", err)
	}
	users, err := store.RemoteUsers()
	if err != nil || len(users) != 2 {
		t.Fatalf("users: %+v %v", users, err)
	}
}

func TestRemoteCredentialValidation(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, tc := range []struct{ name, token string }{
		{"", strings.Repeat("x", 32)}, {" ", strings.Repeat("x", 32)}, {"name", "short"},
		{"name\n", strings.Repeat("x", 32)}, {"name", strings.Repeat("x", 32) + "\r"}, {"name:invalid", strings.Repeat("x", 32)},
	} {
		if _, err := store.ProvisionUser(tc.name, tc.token); err == nil {
			t.Fatalf("invalid credential accepted for %q", tc.name)
		}
	}
}
