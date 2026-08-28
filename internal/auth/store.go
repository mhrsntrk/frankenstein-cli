// Package auth handles Proton credentials: the SRP login with its human
// verification detour, key unlocking, and persisting the session so later runs
// skip the captcha and 2FA.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
)

const (
	keyringService = "frankenstein-cli"
	keyringUser    = "proton-session"
)

// ErrNoSession means nothing has been stored yet.
var ErrNoSession = errors.New("no stored session")

// Session is what a later run needs to resume without a full login.
//
// Proton rotates the refresh token on every use, so this must be rewritten
// each time the client reports an auth change. A stale copy is a dead session.
type Session struct {
	UID          string `json:"uid"`
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id,omitempty"`
	Username     string `json:"username,omitempty"`

	// SaltedKeyPass is the derived key passphrase. Storing it is what lets the
	// tool run unattended; without it every command would prompt for the
	// mailbox password. It is a secret equivalent to the mailbox password for
	// this account's data, so it belongs in the keyring, and the file fallback
	// is written 0600.
	SaltedKeyPass []byte `json:"salted_key_pass,omitempty"`
}

// Valid reports whether the session has enough to attempt a resume.
func (s Session) Valid() bool { return s.UID != "" && s.RefreshToken != "" }

// Store persists sessions in the system keyring, falling back to a file when
// no keyring is available (headless Linux, containers).
type Store struct {
	// fallbackPath is used when the keyring is unavailable.
	fallbackPath string

	// Warn, when set, receives non-fatal notices — currently only "the
	// keyring refused the write, the session went to the fallback file".
	// It is a field rather than an extra return value because Save is also
	// called from the token-rotation callback, where there is nobody to
	// hand a second return to. Nil means the notice is dropped.
	Warn func(msg string)
}

// NewStore returns a session store.
func NewStore() (*Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}

	return &Store{fallbackPath: filepath.Join(dir, "credentials.json")}, nil
}

// Save writes the session, preferring the keyring.
func (s *Store) Save(sess Session) error {
	b, err := json.Marshal(sess)
	if err != nil {
		return err
	}

	kerr := keyring.Set(keyringService, keyringUser, string(b))
	if kerr == nil {
		// Belt and braces: if a fallback file exists from an earlier run
		// without a keyring, remove it rather than leaving a stale secret.
		_ = os.Remove(s.fallbackPath)

		return nil
	}

	// The fallback below keeps the session alive, but the user should know
	// their secret now lives on disk and that the keyring is misbehaving.
	if s.Warn != nil {
		s.Warn(fmt.Sprintf("keyring unavailable (%v); storing session in %s", kerr, s.fallbackPath))
	}

	if err := os.MkdirAll(filepath.Dir(s.fallbackPath), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if err := os.WriteFile(s.fallbackPath, b, 0o600); err != nil {
		return fmt.Errorf("write credentials fallback: %w", err)
	}

	return nil
}

// Load reads the stored session, returning ErrNoSession when there is none.
func (s *Store) Load() (Session, error) {
	raw, kerr := keyring.Get(keyringService, keyringUser)
	if kerr == nil {
		var sess Session

		if err := json.Unmarshal([]byte(raw), &sess); err != nil {
			return Session{}, fmt.Errorf("parse keyring session: %w", err)
		}

		return sess, nil
	}

	// Any keyring failure falls through to the file, because a session saved
	// while the keyring was down lives there. The keyring error still
	// matters when the file is also absent: see below.
	b, err := os.ReadFile(s.fallbackPath)
	if os.IsNotExist(err) {
		// "Not found" from the keyring plus no fallback file is a clean
		// first run. Anything else from the keyring is a real keychain
		// failure — a locked or corrupt keychain, a denied prompt — and
		// reporting it as "no session" would send the user through a full
		// login whose Save is likely to hit the same wall.
		if !errors.Is(kerr, keyring.ErrNotFound) {
			return Session{}, fmt.Errorf("read keyring session: %w", kerr)
		}

		return Session{}, ErrNoSession
	}

	if err != nil {
		return Session{}, fmt.Errorf("read credentials fallback: %w", err)
	}

	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return Session{}, fmt.Errorf("parse credentials fallback: %w", err)
	}

	return sess, nil
}

// Clear removes the stored session from both locations.
func (s *Store) Clear() error {
	// Ignore "not found" from either side; the goal is that nothing remains.
	_ = keyring.Delete(keyringService, keyringUser)

	if err := os.Remove(s.fallbackPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove credentials fallback: %w", err)
	}

	return nil
}

// Location describes where the session is stored, which is the first thing
// anyone asks when a login does not stick.
func (s *Store) Location() string {
	if _, err := keyring.Get(keyringService, keyringUser); err == nil {
		return "system keyring"
	}

	if _, err := os.Stat(s.fallbackPath); err == nil {
		return s.fallbackPath
	}

	return "not stored"
}
