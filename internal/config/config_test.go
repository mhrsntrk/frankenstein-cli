package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// isolate points the config path into a per-test directory so tests never
// touch the real file.
func isolate(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
}

// rawFile reads the config file straight off disk as JSON.
func rawFile(t *testing.T) map[string]any {
	t.Helper()

	path, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	return m
}

// writeFile puts a hand-built config on disk, creating the directory.
func writeFile(t *testing.T, content string, perm os.FileMode) {
	t.Helper()

	path, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	isolate(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !reflect.DeepEqual(cfg, Defaults()) {
		t.Fatalf("Load = %+v, want Defaults %+v", cfg, Defaults())
	}
}

func TestRoundTripKeepsUserValues(t *testing.T) {
	isolate(t)

	cfg := Defaults()
	cfg.Username = "m@example.com"
	cfg.BodyCacheSize = 1000
	cfg.Calendar.ClientSecret = "sekrit"

	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Username != cfg.Username ||
		got.BodyCacheSize != cfg.BodyCacheSize ||
		got.Calendar.ClientSecret != cfg.Calendar.ClientSecret {
		t.Fatalf("round trip lost values: got %+v", got)
	}
}

// A config saved by an old binary must not pin that binary's defaults: the
// file only records overrides, so Load with newer defaults yields the newer
// values.
func TestSaveOmitsDefaults(t *testing.T) {
	isolate(t)

	// What the pre-fix Save produced after any login: today's defaults
	// written out verbatim, plus the user's own bits.
	writeFile(t, `{
		"api_host": "`+DefaultAPIHost+`",
		"app_version": "`+DefaultAppVersion+`",
		"body_cache_size": 500,
		"sync_interval": 30,
		"username": "m@example.com",
		"calendar": {"calendar_id": "primary", "client_id": "cid"}
	}`, 0o600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw := rawFile(t)

	for _, k := range []string{"api_host", "app_version", "body_cache_size", "sync_interval"} {
		if _, ok := raw[k]; ok {
			t.Errorf("default-valued %q written to file: %v", k, raw[k])
		}
	}

	if raw["username"] != "m@example.com" {
		t.Errorf("username = %v, want m@example.com", raw["username"])
	}

	// Nested defaults are pruned field by field, not object by object.
	cal, _ := raw["calendar"].(map[string]any)
	if cal == nil {
		t.Fatalf("calendar object missing: %v", raw)
	}

	if _, ok := cal["calendar_id"]; ok {
		t.Errorf("default calendar_id written to file: %v", cal["calendar_id"])
	}

	if cal["client_id"] != "cid" {
		t.Errorf("client_id = %v, want cid", cal["client_id"])
	}
}

func TestExplicitOverrideSurvives(t *testing.T) {
	isolate(t)

	cfg := Defaults()
	cfg.AppVersion = "macos-bridge@9.9.9"
	cfg.APIHost = "https://example.test/api"

	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.AppVersion != cfg.AppVersion || got.APIHost != cfg.APIHost {
		t.Fatalf("override lost: got %q %q", got.AppVersion, got.APIHost)
	}

	raw := rawFile(t)
	if raw["app_version"] != cfg.AppVersion || raw["api_host"] != cfg.APIHost {
		t.Fatalf("override not on disk: %v", raw)
	}
}

func TestUnknownKeysSurviveSave(t *testing.T) {
	isolate(t)

	writeFile(t, `{
		"username": "m@example.com",
		"future_field": "keep me",
		"calendar": {"client_id": "cid", "future_nested": 7}
	}`, 0o600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Username = "new@example.com"
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw := rawFile(t)

	if raw["future_field"] != "keep me" {
		t.Errorf("future_field = %v, want keep me", raw["future_field"])
	}

	cal, _ := raw["calendar"].(map[string]any)
	if cal == nil || cal["future_nested"] != float64(7) {
		t.Errorf("nested unknown key lost: %v", raw["calendar"])
	}

	if raw["username"] != "new@example.com" {
		t.Errorf("username = %v, want new@example.com", raw["username"])
	}
}

func TestSaveTightensPerms(t *testing.T) {
	isolate(t)

	writeFile(t, `{"calendar": {"client_secret": "sekrit"}}`, 0o644)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}

func TestLoadReplacesNonPositiveValues(t *testing.T) {
	isolate(t)

	writeFile(t, `{"body_cache_size": -1, "sync_interval": -5}`, 0o600)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	d := Defaults()
	if cfg.BodyCacheSize != d.BodyCacheSize || cfg.SyncInterval != d.SyncInterval {
		t.Fatalf("negative values not replaced: %d %d", cfg.BodyCacheSize, cfg.SyncInterval)
	}
}
