package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// These tests swap in go-keyring's in-memory mock so nothing touches the real
// keychain. The mock is package-global state, so none of them run in
// parallel.

func testStore(t *testing.T) *Store {
	t.Helper()

	return &Store{fallbackPath: filepath.Join(t.TempDir(), "credentials.json")}
}

func testSession() Session {
	return Session{
		UID:           "uid-1",
		RefreshToken:  "refresh-1",
		UserID:        "user-1",
		Username:      "m",
		SaltedKeyPass: []byte("salted"),
	}
}

func TestStoreKeyringRoundTrip(t *testing.T) {
	keyring.MockInit()

	s := testStore(t)

	if err := s.Save(testSession()); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The keyring took the write, so no secret may land on disk.
	if _, err := os.Stat(s.fallbackPath); !os.IsNotExist(err) {
		t.Errorf("fallback file exists after a successful keyring save")
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.UID != "uid-1" || got.RefreshToken != "refresh-1" || string(got.SaltedKeyPass) != "salted" {
		t.Errorf("round-trip mangled the session: %+v", got)
	}
}

func TestStoreFileFallbackWarnsAndRoundTrips(t *testing.T) {
	keyring.MockInitWithError(errors.New("keychain locked"))

	s := testStore(t)

	var warned string

	s.Warn = func(msg string) { warned = msg }

	if err := s.Save(testSession()); err != nil {
		t.Fatalf("save with dead keyring: %v", err)
	}

	if !strings.Contains(warned, "keychain locked") || !strings.Contains(warned, s.fallbackPath) {
		t.Errorf("warning %q does not name the keyring error and the fallback path", warned)
	}

	info, err := os.Stat(s.fallbackPath)
	if err != nil {
		t.Fatalf("fallback file: %v", err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("fallback file mode = %o, want 600", perm)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("load from fallback: %v", err)
	}

	if got.UID != "uid-1" || string(got.SaltedKeyPass) != "salted" {
		t.Errorf("fallback round-trip mangled the session: %+v", got)
	}
}

func TestStoreLoadNoSession(t *testing.T) {
	keyring.MockInit()

	if _, err := testStore(t).Load(); !errors.Is(err, ErrNoSession) {
		t.Errorf("load on empty store = %v, want ErrNoSession", err)
	}
}

func TestStoreLoadSurfacesKeyringFailure(t *testing.T) {
	// A real keychain failure with no fallback file must not be reported as
	// "no session": that answer sends the user into a login whose save
	// would hit the same broken keychain.
	keyring.MockInitWithError(errors.New("keychain exploded"))

	_, err := testStore(t).Load()

	if errors.Is(err, ErrNoSession) {
		t.Fatalf("keyring failure masqueraded as ErrNoSession")
	}

	if err == nil || !strings.Contains(err.Error(), "keychain exploded") {
		t.Errorf("load = %v, want the keyring error surfaced", err)
	}
}

func TestStoreClearRemovesFallback(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))

	s := testStore(t)

	if err := s.Save(testSession()); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if _, err := os.Stat(s.fallbackPath); !os.IsNotExist(err) {
		t.Errorf("fallback file survived Clear")
	}
}
