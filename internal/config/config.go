// Package config owns the on-disk layout and the few knobs the tool exposes.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AppName is the binary and config directory name.
const AppName = "frankenstein"

// DefaultAppVersion is the x-pm-appversion header sent to Proton.
//
// Proton refuses any identifier that is not one of its own clients: the
// library's own default gets 400 "Platform `go` is not valid", and an
// out-of-date version gets 422 code 5003. There is no third-party identity to
// use, so we present as Bridge. This pin will eventually expire and need
// bumping; see README.md.
const DefaultAppVersion = "macos-bridge@3.26.0"

// DefaultAPIHost is Proton's API root.
const DefaultAPIHost = "https://mail.proton.me/api"

// Config is the user-editable configuration.
type Config struct {
	// APIHost and AppVersion override the Proton endpoint and client identity.
	APIHost    string `json:"api_host,omitempty"`
	AppVersion string `json:"app_version,omitempty"`

	// Username is remembered so login only has to prompt for secrets.
	Username string `json:"username,omitempty"`

	// BodyCacheSize is how many decrypted bodies to keep. The cache is warm,
	// not a mirror; older bodies are evicted by last access.
	BodyCacheSize int `json:"body_cache_size,omitempty"`

	// SyncInterval is how often the background sync polls, in seconds.
	SyncInterval int `json:"sync_interval,omitempty"`

	// Screener holds the box IDs the HEY layer routes into. Empty until
	// `frankenstein screener setup` has run.
	Screener ScreenerConfig `json:"screener,omitempty"`

	// Calendar is the Google Calendar configuration.
	Calendar CalendarConfig `json:"calendar,omitempty"`
}

// ScreenerConfig records which provider boxes back each HEY-style box.
type ScreenerConfig struct {
	ImboxID       string `json:"imbox_id,omitempty"`
	FeedID        string `json:"feed_id,omitempty"`
	PaperTrailID  string `json:"paper_trail_id,omitempty"`
	ScreenedOutID string `json:"screened_out_id,omitempty"`

	// Enabled turns automatic routing on. Screening decisions are still
	// recorded when off.
	Enabled bool `json:"enabled,omitempty"`
}

// Configured reports whether the screener boxes exist.
func (s ScreenerConfig) Configured() bool {
	return s.ImboxID != "" && s.FeedID != "" && s.PaperTrailID != "" && s.ScreenedOutID != ""
}

// CalendarConfig holds the Google OAuth client. The token itself lives in the
// keyring, never here.
//
// Leaving ClientID empty falls back to whatever the build was compiled with,
// so a distributed binary can carry one while a build from source asks for the
// user's own.
type CalendarConfig struct {
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`

	// CalendarID defaults to "primary".
	CalendarID string `json:"calendar_id,omitempty"`
}

// Defaults returns a config with every zero value filled in.
func Defaults() Config {
	return Config{
		APIHost:       DefaultAPIHost,
		AppVersion:    DefaultAppVersion,
		BodyCacheSize: 500,
		SyncInterval:  30,
		Calendar:      CalendarConfig{CalendarID: "primary"},
	}
}

// Dir is the configuration directory, honouring XDG_CONFIG_HOME.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("locate config dir: %w", err)
		}

		base = filepath.Join(home, ".config")
	}

	return filepath.Join(base, AppName), nil
}

// DataDir is where the cache and journal live.
func DataDir() (string, error) {
	if d := os.Getenv("FRANKENSTEIN_DATA_DIR"); d != "" {
		return d, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}

	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, AppName), nil
	}

	return filepath.Join(home, ".local", "share", AppName), nil
}

// Path is the config file location.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "config.json"), nil
}

// CachePath is the SQLite cache location.
func CachePath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}

	return filepath.Join(dir, "cache.db"), nil
}

// JournalDir is where journal entries are written as markdown.
func JournalDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}

	out := filepath.Join(dir, "journal")
	if err := os.MkdirAll(out, 0o700); err != nil {
		return "", fmt.Errorf("create journal dir: %w", err)
	}

	return out, nil
}

// Load reads the config, filling in defaults for anything unset. A missing
// file is not an error.
func Load() (Config, error) {
	cfg := Defaults()

	path, err := Path()
	if err != nil {
		return cfg, err
	}

	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}

	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}

	// Re-apply defaults for fields the file left empty.
	d := Defaults()

	if cfg.APIHost == "" {
		cfg.APIHost = d.APIHost
	}

	if cfg.AppVersion == "" {
		cfg.AppVersion = d.AppVersion
	}

	if cfg.BodyCacheSize == 0 {
		cfg.BodyCacheSize = d.BodyCacheSize
	}

	if cfg.SyncInterval == 0 {
		cfg.SyncInterval = d.SyncInterval
	}

	if cfg.Calendar.CalendarID == "" {
		cfg.Calendar.CalendarID = d.Calendar.CalendarID
	}

	return cfg, nil
}

// Save writes the config, creating the directory if needed.
func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}
