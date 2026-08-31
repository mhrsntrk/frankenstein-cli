// Package config owns the on-disk layout and the few knobs the tool exposes.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

	// Calendar is the Google Calendar configuration.
	Calendar CalendarConfig `json:"calendar,omitempty"`
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

	// CalendarID is the calendar new events are written to. It defaults to
	// "primary".
	CalendarID string `json:"calendar_id,omitempty"`

	// CalendarIDs are the calendars shown. Empty means just CalendarID, which
	// is what an older config looks like.
	CalendarIDs []string `json:"calendar_ids,omitempty"`
}

// Shown is the set of calendars to display, falling back to the single one an
// older config knows about.
func (c CalendarConfig) Shown() []string {
	if len(c.CalendarIDs) > 0 {
		return c.CalendarIDs
	}

	if c.CalendarID != "" {
		return []string{c.CalendarID}
	}

	return []string{"primary"}
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

// NotesDir is where quick notes are written as markdown. The directory is
// created lazily by the notes store, so a machine that never writes one never
// grows the folder.
func NotesDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "notes"), nil
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

	// Re-apply defaults for fields the file left empty. A zero or negative
	// size or interval is meaningless, so it is silently treated as unset
	// rather than rejected; hand-editing the file should never brick the tool.
	d := Defaults()

	if cfg.APIHost == "" {
		cfg.APIHost = d.APIHost
	}

	if cfg.AppVersion == "" {
		cfg.AppVersion = d.AppVersion
	}

	if cfg.BodyCacheSize <= 0 {
		cfg.BodyCacheSize = d.BodyCacheSize
	}

	if cfg.SyncInterval <= 0 {
		cfg.SyncInterval = d.SyncInterval
	}

	if cfg.Calendar.CalendarID == "" {
		cfg.Calendar.CalendarID = d.Calendar.CalendarID
	}

	return cfg, nil
}

// Save writes the config, creating the directory if needed.
//
// Only values that differ from Defaults reach the file. Load fills every
// gap, so writing the defaults back would freeze them: a config saved today
// would pin today's api_host and app_version forever, and existing users
// would never pick up a new binary's bump (the app-version pin above is
// exactly the field that has to move). Keys this binary does not know about
// are carried over from the existing file untouched, so a newer binary's
// fields survive a save by an older one.
func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Start from whatever is on disk so unknown keys survive the round trip.
	out := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(b, &out) != nil {
			out = map[string]any{}
		}
	}

	cur, err := toMap(cfg)
	if err != nil {
		return err
	}

	def, err := toMap(Defaults())
	if err != nil {
		return err
	}

	mergeKnown(out, cur, def, reflect.TypeOf(cfg))

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// WriteFile only applies the mode to a new file. The config can hold the
	// calendar client secret, so tighten a pre-existing looser file too.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod config: %w", err)
	}

	return nil
}

// toMap round-trips a config through JSON so it can be compared and merged
// key by key.
func toMap(cfg Config) (map[string]any, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}

	return m, nil
}

// mergeKnown rewrites out's known fields from cfg, dropping any whose value
// matches def so the file only records what the user actually set. Keys the
// struct does not declare are left alone. Nested structs are merged the same
// way, field by field, so overriding one calendar field does not freeze the
// rest.
func mergeKnown(out, cfg, def map[string]any, t reflect.Type) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}

		if f.Type.Kind() == reflect.Struct {
			sub, _ := out[name].(map[string]any)
			if sub == nil {
				sub = map[string]any{}
			}

			cs, _ := cfg[name].(map[string]any)
			ds, _ := def[name].(map[string]any)
			mergeKnown(sub, cs, ds, f.Type)

			if len(sub) == 0 {
				delete(out, name)
			} else {
				out[name] = sub
			}

			continue
		}

		v, ok := cfg[name]
		if !ok || reflect.DeepEqual(v, def[name]) {
			delete(out, name)
			continue
		}

		out[name] = v
	}
}
