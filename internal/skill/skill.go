// Package skill embeds the agent skill and installs it.
//
// The skill describes this tool's command surface to a coding agent. It is
// embedded rather than fetched so the installed copy always matches the binary
// that installed it.
package skill

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed assets/SKILL.md
var assets embed.FS

// Name is the directory the skill installs into.
const Name = "frankenstein"

// Content returns the skill markdown.
func Content() ([]byte, error) {
	return assets.ReadFile("assets/SKILL.md")
}

// DefaultDir is where agent skills live for the current user.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}

	return filepath.Join(home, ".claude", "skills", Name), nil
}

// Install writes the skill. It refuses to overwrite unless force is set, so a
// user who has edited their copy does not lose it to a routine upgrade.
func Install(dir string, force bool) (string, error) {
	if dir == "" {
		var err error

		dir, err = DefaultDir()
		if err != nil {
			return "", err
		}
	}

	path := filepath.Join(dir, "SKILL.md")

	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("%s already exists; pass --force to replace it", path)
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}

	content, err := Content()
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", fmt.Errorf("write skill: %w", err)
	}

	return path, nil
}
