package reviewstore

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var ErrAuthentication = errors.New("invalid credentials")

func initializeAuthentication(transaction *sql.Tx) error {
	_, err := transaction.Exec(`CREATE TABLE IF NOT EXISTS remote_credentials (
		user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		account TEXT NOT NULL UNIQUE, token_hash TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL)`)
	return err
}

func validCredentials(account, token string) bool {
	return strings.TrimSpace(account) != "" && len(account) <= 256 && !strings.ContainsAny(account, "\r\n:") && len(token) >= 32 && !strings.ContainsAny(token, "\r\n")
}

func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// EnsureRemoteUser bootstraps credentials, but never changes existing credentials.
func (store *Store) EnsureRemoteUser(account, token string) (User, error) {
	return store.provisionRemoteUser(account, token, true)
}

// ProvisionUser explicitly creates a new remote account. Tokens are stored hashed.
func (store *Store) ProvisionUser(account, token string) (User, error) {
	return store.provisionRemoteUser(account, token, false)
}

func (store *Store) provisionRemoteUser(account, token string, reuse bool) (User, error) {
	if !validCredentials(account, token) {
		return User{}, errors.New("remote account requires a name and a token of at least 32 bytes")
	}
	transaction, err := store.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer transaction.Rollback()
	var user User
	var existing string
	err = transaction.QueryRow(`SELECT u.id, u.name, c.token_hash FROM remote_credentials c JOIN users u ON u.id=c.user_id WHERE c.account=?`, account).Scan(&user.ID, &user.Name, &existing)
	if err == nil {
		if !reuse {
			return User{}, errors.New("remote account already exists")
		}
		if subtle.ConstantTimeCompare([]byte(existing), []byte(tokenHash(token))) != 1 {
			return User{}, errors.New("configured token differs from persisted account credential")
		}
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	var duplicate int
	if err := transaction.QueryRow(`SELECT COUNT(*) FROM remote_credentials WHERE token_hash=?`, tokenHash(token)).Scan(&duplicate); err != nil {
		return User{}, err
	}
	if duplicate != 0 {
		return User{}, errors.New("credential is already assigned to an account")
	}
	id, err := newID()
	if err != nil {
		return User{}, err
	}
	// Retain the owner identity used by legacy single-account deployments.
	if _, err := transaction.Exec(`INSERT INTO users(id,os_uid,name,created_at) VALUES(?,?,?,?) ON CONFLICT(os_uid) DO NOTHING`, id, "account:"+account, account, time.Now().UnixMilli()); err != nil {
		return User{}, err
	}
	if err := transaction.QueryRow(`SELECT id,name FROM users WHERE os_uid=?`, "account:"+account).Scan(&user.ID, &user.Name); err != nil {
		return User{}, err
	}
	if _, err := transaction.Exec(`INSERT INTO remote_credentials(user_id,account,token_hash,created_at) VALUES(?,?,?,?)`, user.ID, account, tokenHash(token), time.Now().UnixMilli()); err != nil {
		return User{}, err
	}
	return user, transaction.Commit()
}

func (store *Store) AuthenticateToken(token string) (User, error) {
	var user User
	err := store.db.QueryRow(`SELECT u.id,c.account FROM remote_credentials c JOIN users u ON u.id=c.user_id WHERE c.token_hash=?`, tokenHash(token)).Scan(&user.ID, &user.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrAuthentication
	}
	return user, err
}

func (store *Store) RemoteUsers() ([]User, error) {
	rows, err := store.db.Query(`SELECT u.id,c.account FROM remote_credentials c JOIN users u ON u.id=c.user_id ORDER BY c.account`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Name); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}
